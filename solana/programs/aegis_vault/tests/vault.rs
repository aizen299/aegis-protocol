use aegis_vault::{accounts, events::Role, instruction, state::*};
use anchor_lang::{
    prelude::Pubkey, AccountDeserialize, AnchorSerialize, Discriminator, InstructionData,
    ToAccountMetas,
};
use anchor_spl::token::spl_token;
use litesvm::LiteSVM;
use solana_account::Account;
use solana_keypair::Keypair;
use solana_message::{Instruction, Message};
use solana_program_pack::Pack;
use solana_signer::Signer;
use solana_transaction::Transaction;

const PROGRAM: Pubkey = aegis_vault::ID;
const SYSTEM: Pubkey = anchor_lang::system_program::ID;
const BPF_LOADER_UPGRADEABLE: Pubkey =
    anchor_lang::pubkey!("BPFLoaderUpgradeab1e11111111111111111111111");
const TOKEN_2022: Pubkey = anchor_lang::pubkey!("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb");

struct Env {
    svm: LiteSVM,
    admin: Keypair,
    manager: Keypair,
    pauser: Keypair,
    mint: Pubkey,
    mint_authority: Keypair,
}

fn program_bytes() -> Vec<u8> {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../target/deploy/aegis_vault.so"
    );
    std::fs::read(path).unwrap_or_else(|e| panic!("{path}: {e}; run `anchor build` first"))
}

fn event_authority() -> Pubkey {
    Pubkey::find_program_address(&[b"__event_authority"], &PROGRAM).0
}

fn vault_pda(mint: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[VAULT_SEED, mint.as_ref()], &PROGRAM).0
}

fn tokens_pda(mint: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[TOKENS_SEED, vault_pda(mint).as_ref()], &PROGRAM).0
}

fn position_pda(mint: &Pubkey, owner: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(
        &[POSITION_SEED, vault_pda(mint).as_ref(), owner.as_ref()],
        &PROGRAM,
    )
    .0
}

fn program_data() -> Pubkey {
    Pubkey::find_program_address(&[PROGRAM.as_ref()], &BPF_LOADER_UPGRADEABLE).0
}

fn send(
    svm: &mut LiteSVM,
    ixs: &[Instruction],
    payer: &Keypair,
    signers: &[&Keypair],
) -> Result<Vec<String>, Vec<String>> {
    svm.expire_blockhash();
    let mut all: Vec<&Keypair> = vec![payer];
    all.extend_from_slice(signers);
    let tx = Transaction::new(
        &all,
        Message::new(ixs, Some(&payer.pubkey())),
        svm.latest_blockhash(),
    );
    match svm.send_transaction(tx) {
        Ok(meta) => Ok(meta.logs),
        Err(failed) => Err(failed.meta.logs),
    }
}

#[track_caller]
fn expect_refused(result: Result<Vec<String>, Vec<String>>, reason: &str) {
    match result {
        Ok(_) => panic!("accepted; expected refusal for {reason}"),
        Err(logs) => assert!(
            logs.iter().any(|l| l.contains(reason)),
            "refused, but not for {reason}:\n{}",
            logs.join("\n")
        ),
    }
}

fn new_env() -> Env {
    let mut svm = LiteSVM::new();
    let admin = Keypair::new();
    svm.airdrop(&admin.pubkey(), 100_000_000_000).unwrap();
    svm.add_program(PROGRAM, &program_bytes()).unwrap();
    set_upgrade_authority(&mut svm, Some(admin.pubkey()));

    let mint_authority = Keypair::new();
    let mint = create_mint(&mut svm, &mint_authority, spl_token::ID);
    Env {
        svm,
        admin,
        manager: Keypair::new(),
        pauser: Keypair::new(),
        mint,
        mint_authority,
    }
}

fn set_upgrade_authority(svm: &mut LiteSVM, authority: Option<Pubkey>) {
    let address = program_data();
    let mut account = svm.get_account(&address).unwrap();
    // The loader's bincode header: variant 3 (ProgramData), slot, then Option<Pubkey>.
    let mut header = 3u32.to_le_bytes().to_vec();
    header.extend_from_slice(&0u64.to_le_bytes());
    match authority {
        Some(key) => {
            header.push(1);
            header.extend_from_slice(key.as_ref());
        }
        None => header.extend_from_slice(&[0u8; 33]),
    }
    account.data[..header.len()].copy_from_slice(&header);
    svm.set_account(address, account).unwrap();
}

fn create_mint(svm: &mut LiteSVM, authority: &Keypair, owner: Pubkey) -> Pubkey {
    let mint = Keypair::new();
    let mut data = vec![0u8; spl_token::state::Mint::LEN];
    spl_token::state::Mint {
        mint_authority: Some(authority.pubkey()).into(),
        supply: 0,
        decimals: 6,
        is_initialized: true,
        freeze_authority: None.into(),
    }
    .pack_into_slice(&mut data);
    let lamports = svm.minimum_balance_for_rent_exemption(data.len());
    svm.set_account(
        mint.pubkey(),
        Account {
            lamports,
            data,
            owner,
            executable: false,
            rent_epoch: 0,
        },
    )
    .unwrap();
    mint.pubkey()
}

fn token_account(env: &mut Env, owner: &Pubkey, amount: u64) -> Pubkey {
    let address = Keypair::new().pubkey();
    let mut data = vec![0u8; spl_token::state::Account::LEN];
    spl_token::state::Account {
        mint: env.mint,
        owner: *owner,
        amount,
        delegate: None.into(),
        state: spl_token::state::AccountState::Initialized,
        is_native: None.into(),
        delegated_amount: 0,
        close_authority: None.into(),
    }
    .pack_into_slice(&mut data);
    let lamports = env.svm.minimum_balance_for_rent_exemption(data.len());
    env.svm
        .set_account(
            address,
            Account {
                lamports,
                data,
                owner: spl_token::ID,
                executable: false,
                rent_epoch: 0,
            },
        )
        .unwrap();
    address
}

fn token_balance(svm: &LiteSVM, address: &Pubkey) -> u64 {
    spl_token::state::Account::unpack(&svm.get_account(address).unwrap().data)
        .unwrap()
        .amount
}

fn user(env: &mut Env, amount: u64) -> (Keypair, Pubkey) {
    let kp = Keypair::new();
    env.svm.airdrop(&kp.pubkey(), 10_000_000_000).unwrap();
    let tokens = token_account(env, &kp.pubkey(), amount);
    (kp, tokens)
}

fn initialize_ix(env: &Env, signer: &Pubkey, mint: Pubkey, cap: u64, min: u64) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::InitializeVault {
            admin: *signer,
            vault: vault_pda(&mint),
            vault_tokens: tokens_pda(&mint),
            mint,
            this_program: PROGRAM,
            program_data: program_data(),
            token_program: spl_token::ID,
            system_program: SYSTEM,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::InitializeVault {
            manager: env.manager.pubkey(),
            pauser: env.pauser.pubkey(),
            deposit_cap: cap,
            min_deposit: min,
        }
        .data(),
    }
}

fn initialize(env: &mut Env, cap: u64, min: u64) {
    let ix = initialize_ix(env, &env.admin.pubkey(), env.mint, cap, min);
    let admin = env.admin.insecure_clone();
    send(&mut env.svm, &[ix], &admin, &[]).expect("initialize");
}

fn deposit_ix(
    env: &Env,
    depositor: &Pubkey,
    receiver: &Pubkey,
    from: Pubkey,
    amount: u64,
    min_shares: u128,
) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::Deposit {
            depositor: *depositor,
            receiver: *receiver,
            vault: vault_pda(&env.mint),
            vault_tokens: tokens_pda(&env.mint),
            depositor_tokens: from,
            position: position_pda(&env.mint, receiver),
            token_program: spl_token::ID,
            system_program: SYSTEM,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::Deposit { amount, min_shares }.data(),
    }
}

fn deposit(
    env: &mut Env,
    who: &Keypair,
    from: Pubkey,
    amount: u64,
    min_shares: u128,
) -> Result<Vec<String>, Vec<String>> {
    let ix = deposit_ix(env, &who.pubkey(), &who.pubkey(), from, amount, min_shares);
    send(&mut env.svm, &[ix], who, &[])
}

fn withdraw_ix(
    env: &Env,
    owner: &Pubkey,
    position: Pubkey,
    to: Pubkey,
    shares: u128,
    min_amount: u64,
) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::Withdraw {
            owner: *owner,
            vault: vault_pda(&env.mint),
            vault_tokens: tokens_pda(&env.mint),
            receiver_tokens: to,
            position,
            token_program: spl_token::ID,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::Withdraw { shares, min_amount }.data(),
    }
}

fn withdraw(
    env: &mut Env,
    who: &Keypair,
    to: Pubkey,
    shares: u128,
    min_amount: u64,
) -> Result<Vec<String>, Vec<String>> {
    let ix = withdraw_ix(
        env,
        &who.pubkey(),
        position_pda(&env.mint, &who.pubkey()),
        to,
        shares,
        min_amount,
    );
    send(&mut env.svm, &[ix], who, &[])
}

fn manage(env: &mut Env, signer: &Keypair, data: Vec<u8>) -> Result<Vec<String>, Vec<String>> {
    let ix = Instruction {
        program_id: PROGRAM,
        accounts: accounts::Manage {
            signer: signer.pubkey(),
            vault: vault_pda(&env.mint),
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data,
    };
    let payer = env.admin.insecure_clone();
    send(&mut env.svm, &[ix], &payer, &[signer])
}

fn vault(env: &Env) -> Vault {
    Vault::try_deserialize(
        &mut env
            .svm
            .get_account(&vault_pda(&env.mint))
            .unwrap()
            .data
            .as_slice(),
    )
    .unwrap()
}

fn position(env: &Env, owner: &Pubkey) -> Option<Position> {
    let account = env.svm.get_account(&position_pda(&env.mint, owner))?;
    if account.data.is_empty() {
        return None;
    }
    Some(Position::try_deserialize(&mut account.data.as_slice()).unwrap())
}

// --- creation ---

#[test]
fn the_upgrade_authority_creates_a_vault() {
    let mut env = new_env();
    initialize(&mut env, 5_000_000, 10);

    let v = vault(&env);
    assert_eq!(v.version, VAULT_VERSION);
    assert_eq!(v.mint, env.mint);
    assert_eq!(v.admin, env.admin.pubkey());
    assert_eq!(v.manager, env.manager.pubkey());
    assert_eq!(v.pauser, env.pauser.pubkey());
    assert_eq!(
        (v.deposit_cap, v.min_deposit, v.total_shares),
        (5_000_000, 10, 0)
    );
    assert!(v.pending_admin == Pubkey::default() && !v.paused && !v.withdrawals_frozen);
}

// The Solana-specific exploit: an open initializer lets anyone create the vault first and name
// themselves admin.
#[test]
fn nobody_else_can_create_a_vault() {
    let mut env = new_env();
    let attacker = Keypair::new();
    env.svm.airdrop(&attacker.pubkey(), 10_000_000_000).unwrap();

    let ix = initialize_ix(&env, &attacker.pubkey(), env.mint, 0, 0);
    expect_refused(
        send(&mut env.svm, &[ix], &attacker, &[]),
        "NotUpgradeAuthority",
    );
}

// The upgrade authority must be this program's. An attacker who is the authority of a program they
// deployed must not pass that program's data account instead.
#[test]
fn another_programs_upgrade_authority_cannot_create_a_vault() {
    let mut env = new_env();
    let attacker = Keypair::new();
    env.svm.airdrop(&attacker.pubkey(), 10_000_000_000).unwrap();

    let decoy = Keypair::new().pubkey();
    env.svm.add_program(decoy, &program_bytes()).unwrap();
    let decoy_data = Pubkey::find_program_address(&[decoy.as_ref()], &BPF_LOADER_UPGRADEABLE).0;
    let mut account = env.svm.get_account(&decoy_data).unwrap();
    account.data[12] = 1;
    account.data[13..45].copy_from_slice(attacker.pubkey().as_ref());
    env.svm.set_account(decoy_data, account).unwrap();

    let mut ix = initialize_ix(&env, &attacker.pubkey(), env.mint, 0, 0);
    let slot = ix
        .accounts
        .iter()
        .position(|m| m.pubkey == program_data())
        .unwrap();
    ix.accounts[slot].pubkey = decoy_data;
    expect_refused(
        send(&mut env.svm, &[ix], &attacker, &[]),
        "NotUpgradeAuthority",
    );
}

#[test]
fn a_program_with_no_upgrade_authority_cannot_create_a_vault() {
    let mut env = new_env();
    set_upgrade_authority(&mut env.svm, None);
    let ix = initialize_ix(&env, &env.admin.pubkey(), env.mint, 0, 0);
    let admin = env.admin.insecure_clone();
    expect_refused(
        send(&mut env.svm, &[ix], &admin, &[]),
        "NotUpgradeAuthority",
    );
}

#[test]
fn a_second_vault_for_the_same_mint_is_refused() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let ix = initialize_ix(&env, &env.admin.pubkey(), env.mint, 0, 0);
    let admin = env.admin.insecure_clone();
    expect_refused(send(&mut env.svm, &[ix], &admin, &[]), "already in use");
}

// A Token-2022 mint can take a transfer fee, so a transfer of `amount` may not deliver it.
#[test]
fn a_token_2022_mint_is_refused() {
    let mut env = new_env();
    let admin = env.admin.insecure_clone();
    let authority = env.mint_authority.insecure_clone();
    let mint = create_mint(&mut env.svm, &authority, TOKEN_2022);
    let ix = initialize_ix(&env, &admin.pubkey(), mint, 0, 0);
    expect_refused(
        send(&mut env.svm, &[ix], &admin, &[]),
        "AccountOwnedByWrongProgram",
    );
}

// --- deposits and withdrawals ---

#[test]
fn a_deposit_and_full_withdrawal_round_trip() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, alice_tokens) = user(&mut env, 1_000_000);

    deposit(&mut env, &alice, alice_tokens, 1_000_000, 1_000_000_000).expect("deposit");
    assert_eq!(
        position(&env, &alice.pubkey()).unwrap().shares,
        1_000_000_000
    );
    assert_eq!(vault(&env).total_shares, 1_000_000_000);
    assert_eq!(token_balance(&env.svm, &tokens_pda(&env.mint)), 1_000_000);
    assert_eq!(token_balance(&env.svm, &alice_tokens), 0);

    withdraw(&mut env, &alice, alice_tokens, 1_000_000_000, 1_000_000).expect("withdraw");
    assert_eq!(token_balance(&env.svm, &alice_tokens), 1_000_000);
    assert_eq!(position(&env, &alice.pubkey()).unwrap().shares, 0);
    assert_eq!(vault(&env).total_shares, 0);
}

#[test]
fn a_deposit_can_credit_another_receiver() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, alice_tokens) = user(&mut env, 1_000);
    let bob = Keypair::new();

    let ix = deposit_ix(&env, &alice.pubkey(), &bob.pubkey(), alice_tokens, 1_000, 0);
    send(&mut env.svm, &[ix], &alice, &[]).expect("deposit");

    assert_eq!(position(&env, &bob.pubkey()).unwrap().owner, bob.pubkey());
    assert!(position(&env, &alice.pubkey()).is_none());
}

// The indexer reads events from inner instructions, not logs, because RPC nodes truncate logs.
#[test]
fn a_deposit_records_its_event_as_cpi_data() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, alice_tokens) = user(&mut env, 500);

    env.svm.expire_blockhash();
    let ix = deposit_ix(&env, &alice.pubkey(), &alice.pubkey(), alice_tokens, 500, 0);
    let tx = Transaction::new(
        &[&alice],
        Message::new(&[ix], Some(&alice.pubkey())),
        env.svm.latest_blockhash(),
    );
    let meta = env.svm.send_transaction(tx).expect("deposit");

    let expected = aegis_vault::events::Deposited {
        user: alice.pubkey(),
        asset: env.mint,
        amount: 500,
        shares: 500_000,
    };
    let mut encoded = anchor_lang::event::EVENT_IX_TAG_LE.to_vec();
    encoded.extend_from_slice(aegis_vault::events::Deposited::DISCRIMINATOR);
    expected.serialize(&mut encoded).unwrap();

    let found = meta
        .inner_instructions
        .iter()
        .flatten()
        .any(|inner| inner.instruction.data == encoded);
    assert!(found, "no Deposited event among the inner instructions");
    assert!(
        !meta.logs.iter().any(|l| l.starts_with("Program data:")),
        "the event was also written to the logs"
    );
}

#[test]
fn a_zero_deposit_is_refused() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 100);
    expect_refused(deposit(&mut env, &alice, tokens, 0, 0), "ZeroAmount");
}

#[test]
fn a_deposit_below_the_minimum_is_refused() {
    let mut env = new_env();
    initialize(&mut env, 0, 100);
    let (alice, tokens) = user(&mut env, 1_000);
    expect_refused(
        deposit(&mut env, &alice, tokens, 99, 0),
        "DepositBelowMinimum",
    );
    deposit(&mut env, &alice, tokens, 100, 0).expect("the minimum itself is allowed");
}

#[test]
fn a_deposit_over_the_cap_is_refused() {
    let mut env = new_env();
    initialize(&mut env, 1_000, 0);
    let (alice, tokens) = user(&mut env, 2_000);
    deposit(&mut env, &alice, tokens, 600, 0).expect("under the cap");
    expect_refused(
        deposit(&mut env, &alice, tokens, 401, 0),
        "DepositCapExceeded",
    );
    deposit(&mut env, &alice, tokens, 400, 0).expect("exactly the cap");
}

#[test]
fn a_deposit_below_its_share_floor_is_refused_and_moves_nothing() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 1_000);
    expect_refused(
        deposit(&mut env, &alice, tokens, 1_000, 1_000_001),
        "SlippageExceeded",
    );
    assert_eq!(token_balance(&env.svm, &tokens), 1_000);
    assert!(position(&env, &alice.pubkey()).is_none());
}

#[test]
fn a_withdrawal_below_its_amount_floor_is_refused() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 1_000);
    deposit(&mut env, &alice, tokens, 1_000, 0).unwrap();
    expect_refused(
        withdraw(&mut env, &alice, tokens, 1_000_000, 1_001),
        "SlippageExceeded",
    );
}

#[test]
fn withdrawing_more_shares_than_held_is_refused() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 1_000);
    deposit(&mut env, &alice, tokens, 1_000, 0).unwrap();
    expect_refused(
        withdraw(&mut env, &alice, tokens, 1_000_001, 0),
        "InsufficientShares",
    );
}

#[test]
fn nobody_can_withdraw_from_another_holders_position() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, alice_tokens) = user(&mut env, 1_000);
    let (mallory, mallory_tokens) = user(&mut env, 0);
    deposit(&mut env, &alice, alice_tokens, 1_000, 0).unwrap();

    let ix = withdraw_ix(
        &env,
        &mallory.pubkey(),
        position_pda(&env.mint, &alice.pubkey()),
        mallory_tokens,
        1_000_000,
        0,
    );
    expect_refused(send(&mut env.svm, &[ix], &mallory, &[]), "ConstraintSeeds");
    assert_eq!(token_balance(&env.svm, &mallory_tokens), 0);
}

// Burning shares for a transfer to itself would hand the holder's assets to everyone else. Anchor
// refuses the same mutable account twice; this pins that the refusal survives a framework change.
#[test]
fn withdrawing_into_the_vaults_own_token_account_is_refused() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 1_000);
    deposit(&mut env, &alice, tokens, 1_000, 0).unwrap();
    let vault_tokens = tokens_pda(&env.mint);
    expect_refused(
        withdraw(&mut env, &alice, vault_tokens, 1_000_000, 0),
        "ConstraintDuplicateMutableAccount",
    );
}

// The first-depositor attack, end to end: deposit 1, donate directly to the vault's token account,
// and hope the next depositor rounds to zero shares.
#[test]
fn a_donation_does_not_steal_the_next_deposit() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (attacker, attacker_tokens) = user(&mut env, 1_000_001);
    let (victim, victim_tokens) = user(&mut env, 1_000_000);

    deposit(&mut env, &attacker, attacker_tokens, 1, 0).unwrap();
    let donation = spl_token::instruction::transfer(
        &spl_token::ID,
        &attacker_tokens,
        &tokens_pda(&env.mint),
        &attacker.pubkey(),
        &[],
        1_000_000,
    )
    .unwrap();
    send(&mut env.svm, &[donation], &attacker, &[]).unwrap();

    deposit(&mut env, &victim, victim_tokens, 1_000_000, 1)
        .expect("the victim still receives shares");

    let attacker_shares = position(&env, &attacker.pubkey()).unwrap().shares;
    withdraw(&mut env, &attacker, attacker_tokens, attacker_shares, 0).unwrap();
    assert!(
        token_balance(&env.svm, &attacker_tokens) < 1_000_001,
        "the attacker ended with {} of the 1000001 they put in",
        token_balance(&env.svm, &attacker_tokens)
    );
}

// --- controls ---

#[test]
fn a_paused_vault_refuses_deposits_but_not_withdrawals() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 1_000);
    deposit(&mut env, &alice, tokens, 500, 0).unwrap();

    let pauser = env.pauser.insecure_clone();
    manage(
        &mut env,
        &pauser,
        instruction::SetPaused { paused: true }.data(),
    )
    .unwrap();
    expect_refused(deposit(&mut env, &alice, tokens, 500, 0), "Paused");
    withdraw(&mut env, &alice, tokens, 500_000, 0).expect("withdrawals continue while paused");

    manage(
        &mut env,
        &pauser,
        instruction::SetPaused { paused: false }.data(),
    )
    .unwrap();
    deposit(&mut env, &alice, tokens, 500, 0).expect("deposits resume");
}

#[test]
fn frozen_withdrawals_are_refused() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 1_000);
    deposit(&mut env, &alice, tokens, 1_000, 0).unwrap();

    let admin = env.admin.insecure_clone();
    manage(
        &mut env,
        &admin,
        instruction::SetWithdrawalsFrozen { frozen: true }.data(),
    )
    .unwrap();
    expect_refused(
        withdraw(&mut env, &alice, tokens, 1_000_000, 0),
        "WithdrawalsFrozen",
    );
}

#[test]
fn each_control_refuses_the_wrong_role() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (admin, manager, pauser) = (
        env.admin.insecure_clone(),
        env.manager.insecure_clone(),
        env.pauser.insecure_clone(),
    );
    let outsider = Keypair::new();

    let cases: Vec<(&str, Vec<u8>, Vec<&Keypair>, &Keypair)> = vec![
        (
            "set_deposit_cap",
            instruction::SetDepositCap { new_cap: 1 }.data(),
            vec![&admin, &pauser, &outsider],
            &manager,
        ),
        (
            "set_min_deposit",
            instruction::SetMinDeposit { new_min: 1 }.data(),
            vec![&admin, &pauser, &outsider],
            &manager,
        ),
        (
            "set_paused",
            instruction::SetPaused { paused: true }.data(),
            vec![&admin, &manager, &outsider],
            &pauser,
        ),
        (
            "set_withdrawals_frozen",
            instruction::SetWithdrawalsFrozen { frozen: true }.data(),
            vec![&manager, &pauser, &outsider],
            &admin,
        ),
        (
            "set_role",
            instruction::SetRole {
                role: Role::Pauser,
                account: outsider.pubkey(),
            }
            .data(),
            vec![&manager, &pauser, &outsider],
            &admin,
        ),
        (
            "propose_admin",
            instruction::ProposeAdmin {
                proposed: outsider.pubkey(),
            }
            .data(),
            vec![&manager, &pauser, &outsider],
            &admin,
        ),
    ];

    for (name, data, wrong, right) in cases {
        for signer in wrong {
            let result = manage(&mut env, signer, data.clone());
            assert!(result.is_err(), "{name} accepted from {}", signer.pubkey());
            expect_refused(result, "Unauthorized");
        }
        manage(&mut env, right, data)
            .unwrap_or_else(|logs| panic!("{name} refused its own role:\n{}", logs.join("\n")));
    }
}

// The zero key is how an empty pending admin is stored, and no one holds its private key: a role
// set to it is a role no one can exercise.
#[test]
fn no_role_can_be_given_to_the_zero_key() {
    let mut env = new_env();
    let zero = Pubkey::default();

    let mut ix = initialize_ix(&env, &env.admin.pubkey(), env.mint, 0, 0);
    ix.data = instruction::InitializeVault {
        manager: zero,
        pauser: env.pauser.pubkey(),
        deposit_cap: 0,
        min_deposit: 0,
    }
    .data();
    let admin = env.admin.insecure_clone();
    expect_refused(
        send(&mut env.svm, std::slice::from_ref(&ix), &admin, &[]),
        "ZeroAddress",
    );
    ix.data = instruction::InitializeVault {
        manager: env.manager.pubkey(),
        pauser: zero,
        deposit_cap: 0,
        min_deposit: 0,
    }
    .data();
    expect_refused(send(&mut env.svm, &[ix], &admin, &[]), "ZeroAddress");

    initialize(&mut env, 0, 0);
    expect_refused(
        manage(
            &mut env,
            &admin,
            instruction::SetRole {
                role: Role::Pauser,
                account: zero,
            }
            .data(),
        ),
        "ZeroAddress",
    );
    expect_refused(
        manage(
            &mut env,
            &admin,
            instruction::ProposeAdmin { proposed: zero }.data(),
        ),
        "ZeroAddress",
    );

    let (alice, tokens) = user(&mut env, 1_000);
    let ix = deposit_ix(&env, &alice.pubkey(), &zero, tokens, 1_000, 0);
    expect_refused(send(&mut env.svm, &[ix], &alice, &[]), "ZeroAddress");
}

#[test]
fn the_admin_role_cannot_be_set_directly() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let admin = env.admin.insecure_clone();
    let data = instruction::SetRole {
        role: Role::Admin,
        account: Keypair::new().pubkey(),
    }
    .data();
    expect_refused(manage(&mut env, &admin, data), "Unauthorized");
    assert_eq!(vault(&env).admin, admin.pubkey());
}

#[test]
fn the_admin_changes_hands_only_when_the_proposed_key_accepts() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (old, new, other) = (env.admin.insecure_clone(), Keypair::new(), Keypair::new());

    expect_refused(
        manage(&mut env, &new, instruction::AcceptAdmin {}.data()),
        "NoPendingAdmin",
    );

    manage(
        &mut env,
        &old,
        instruction::ProposeAdmin {
            proposed: new.pubkey(),
        }
        .data(),
    )
    .unwrap();
    assert_eq!(
        vault(&env).admin,
        old.pubkey(),
        "proposing alone must not transfer the role"
    );

    expect_refused(
        manage(&mut env, &other, instruction::AcceptAdmin {}.data()),
        "Unauthorized",
    );
    manage(&mut env, &new, instruction::AcceptAdmin {}.data()).unwrap();

    let v = vault(&env);
    assert_eq!(v.admin, new.pubkey());
    assert_eq!(v.pending_admin, Pubkey::default());
    expect_refused(
        manage(
            &mut env,
            &old,
            instruction::SetWithdrawalsFrozen { frozen: true }.data(),
        ),
        "Unauthorized",
    );
}

// --- positions ---

#[test]
fn only_an_empty_position_can_be_closed_and_its_rent_returns() {
    let mut env = new_env();
    initialize(&mut env, 0, 0);
    let (alice, tokens) = user(&mut env, 1_000);
    deposit(&mut env, &alice, tokens, 1_000, 0).unwrap();

    let close = Instruction {
        program_id: PROGRAM,
        accounts: accounts::ClosePosition {
            owner: alice.pubkey(),
            vault: vault_pda(&env.mint),
            position: position_pda(&env.mint, &alice.pubkey()),
        }
        .to_account_metas(None),
        data: instruction::ClosePosition {}.data(),
    };
    expect_refused(
        send(&mut env.svm, std::slice::from_ref(&close), &alice, &[]),
        "PositionNotEmpty",
    );

    withdraw(&mut env, &alice, tokens, 1_000_000, 0).unwrap();
    let before = env.svm.get_balance(&alice.pubkey()).unwrap();
    send(&mut env.svm, &[close], &alice, &[]).expect("close");
    assert!(position(&env, &alice.pubkey()).is_none());
    assert!(
        env.svm.get_balance(&alice.pubkey()).unwrap() > before,
        "rent was not returned"
    );
}
