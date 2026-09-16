use aegis_oracle::{
    accounts, instruction, signature::submission_message, state::*, InitializeArgs, ValueView,
};
use anchor_lang::{
    prelude::Pubkey, AccountDeserialize, AnchorDeserialize, InstructionData, ToAccountMetas,
};
use anchor_spl::token::spl_token;
use litesvm::LiteSVM;
use solana_account::Account;
use solana_clock::Clock;
use solana_keypair::Keypair;
use solana_message::{AccountMeta, Instruction, Message};
use solana_program_pack::Pack;
use solana_signer::Signer;
use solana_transaction::Transaction;

const PROGRAM: Pubkey = aegis_oracle::ID;
const SYSTEM: Pubkey = anchor_lang::system_program::ID;
const BPF_LOADER_UPGRADEABLE: Pubkey =
    anchor_lang::pubkey!("BPFLoaderUpgradeab1e11111111111111111111111");
const ED25519_PROGRAM: Pubkey = anchor_lang::pubkey!("Ed25519SigVerify111111111111111111111111111");
const INSTRUCTIONS_SYSVAR: Pubkey =
    anchor_lang::pubkey!("Sysvar1nstructions1111111111111111111111111");
const TOKEN_2022: Pubkey = anchor_lang::pubkey!("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb");
const CHAIN_ID: i64 = (1 << 62) + 3;
const FEED: [u8; 32] = [0xfe; 32];
const MIN_STAKE: u64 = 1_000;
const FLOOR: u64 = 950;
const ROUND_DURATION: i64 = 60;

type Outcome = Result<Vec<String>, Vec<String>>;
type ArgsCase = (&'static str, Box<dyn Fn(&mut InitializeArgs)>, &'static str);

struct Env {
    svm: LiteSVM,
    admin: Keypair,
    manager: Keypair,
    pauser: Keypair,
    slasher: Keypair,
    mint: Pubkey,
}

fn program_bytes() -> Vec<u8> {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../target/deploy/aegis_oracle.so"
    );
    std::fs::read(path).unwrap_or_else(|e| panic!("{path}: {e}; run `anchor build` first"))
}

fn pda(seeds: &[&[u8]]) -> Pubkey {
    Pubkey::find_program_address(seeds, &PROGRAM).0
}

fn config_pda() -> Pubkey {
    pda(&[CONFIG_SEED])
}
fn vault_pda() -> Pubkey {
    pda(&[STAKE_VAULT_SEED])
}
fn feed_pda() -> Pubkey {
    pda(&[FEED_SEED, &FEED])
}
fn node_pda(node: &Pubkey) -> Pubkey {
    pda(&[NODE_SEED, node.as_ref()])
}
fn round_pda(id: u64) -> Pubkey {
    pda(&[ROUND_SEED, &id.to_le_bytes()])
}
fn submission_pda(round: u64, node: &Pubkey) -> Pubkey {
    pda(&[SUBMISSION_SEED, &round.to_le_bytes(), node.as_ref()])
}
fn slash_pda(round: u64, node: &Pubkey) -> Pubkey {
    pda(&[SLASH_SEED, &round.to_le_bytes(), node.as_ref()])
}
fn event_authority() -> Pubkey {
    pda(&[b"__event_authority"])
}
fn program_data() -> Pubkey {
    Pubkey::find_program_address(&[PROGRAM.as_ref()], &BPF_LOADER_UPGRADEABLE).0
}

fn send(svm: &mut LiteSVM, ixs: &[Instruction], payer: &Keypair, signers: &[&Keypair]) -> Outcome {
    svm.expire_blockhash();
    let mut all = vec![payer];
    all.extend_from_slice(signers);
    let tx = Transaction::new(
        &all,
        Message::new(ixs, Some(&payer.pubkey())),
        svm.latest_blockhash(),
    );
    svm.send_transaction(tx)
        .map(|m| m.logs)
        .map_err(|f| f.meta.logs)
}

#[track_caller]
fn refused(result: Outcome, reason: &str) {
    match result {
        Ok(_) => panic!("accepted; expected refusal for {reason}"),
        Err(logs) => assert!(
            logs.iter().any(|l| l.contains(reason)),
            "refused, but not for {reason}:\n{}",
            logs.join("\n")
        ),
    }
}

fn set_upgrade_authority(svm: &mut LiteSVM, authority: Pubkey) {
    let mut account = svm.get_account(&program_data()).unwrap();
    account.data[4..12].copy_from_slice(&0u64.to_le_bytes());
    account.data[12] = 1;
    account.data[13..45].copy_from_slice(authority.as_ref());
    svm.set_account(program_data(), account).unwrap();
}

fn warp(svm: &mut LiteSVM, seconds: i64) {
    let mut clock: Clock = svm.get_sysvar();
    clock.unix_timestamp += seconds;
    clock.slot += 1;
    svm.set_sysvar(&clock);
}

fn mint_account(svm: &mut LiteSVM, owner: Pubkey) -> Pubkey {
    let mint = Keypair::new().pubkey();
    let mut data = vec![0u8; spl_token::state::Mint::LEN];
    spl_token::state::Mint {
        mint_authority: Some(Pubkey::new_unique()).into(),
        supply: 0,
        decimals: 6,
        is_initialized: true,
        freeze_authority: None.into(),
    }
    .pack_into_slice(&mut data);
    let lamports = svm.minimum_balance_for_rent_exemption(data.len());
    svm.set_account(
        mint,
        Account {
            lamports,
            data,
            owner,
            executable: false,
            rent_epoch: 0,
        },
    )
    .unwrap();
    mint
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

fn balance(svm: &LiteSVM, address: &Pubkey) -> u64 {
    spl_token::state::Account::unpack(&svm.get_account(address).unwrap().data)
        .unwrap()
        .amount
}

fn args(env: &Env) -> InitializeArgs {
    InitializeArgs {
        chain_id: CHAIN_ID,
        oracle_manager: env.manager.pubkey(),
        pauser: env.pauser.pubkey(),
        slasher: env.slasher.pubkey(),
        minimum_stake: MIN_STAKE,
        min_stake_floor: FLOOR,
        unbonding_period: 24 * 60 * 60,
        max_slash_bps: 1_000,
        max_nodes: 4,
        round_duration: ROUND_DURATION,
        quorum_bps: 6_000,
        min_quorum_nodes: 2,
    }
}

fn initialize_ix(signer: &Pubkey, mint: Pubkey, args: InitializeArgs) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::Initialize {
            admin: *signer,
            config: config_pda(),
            stake_vault: vault_pda(),
            stake_mint: mint,
            this_program: PROGRAM,
            program_data: program_data(),
            token_program: spl_token::ID,
            system_program: SYSTEM,
        }
        .to_account_metas(None),
        data: instruction::Initialize { args }.data(),
    }
}

fn uninitialized() -> Env {
    let mut svm = LiteSVM::new();
    let admin = Keypair::new();
    svm.airdrop(&admin.pubkey(), 100_000_000_000).unwrap();
    svm.add_program(PROGRAM, &program_bytes()).unwrap();
    set_upgrade_authority(&mut svm, admin.pubkey());
    let mint = mint_account(&mut svm, spl_token::ID);
    Env {
        svm,
        admin,
        manager: Keypair::new(),
        pauser: Keypair::new(),
        slasher: Keypair::new(),
        mint,
    }
}

fn new_env() -> Env {
    let mut env = uninitialized();
    for k in [
        env.manager.pubkey(),
        env.pauser.pubkey(),
        env.slasher.pubkey(),
    ] {
        env.svm.airdrop(&k, 10_000_000_000).unwrap();
    }
    let ix = initialize_ix(&env.admin.pubkey(), env.mint, args(&env));
    let admin = env.admin.insecure_clone();
    send(&mut env.svm, &[ix], &admin, &[]).expect("initialize");

    let manager = env.manager.insecure_clone();
    send(
        &mut env.svm,
        &[register_feed_ix(&manager.pubkey(), 8)],
        &manager,
        &[],
    )
    .expect("register feed");
    env
}

fn register_feed_ix(manager: &Pubkey, decimals: u8) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::RegisterFeed {
            manager: *manager,
            config: config_pda(),
            feed: feed_pda(),
            system_program: SYSTEM,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::RegisterFeed {
            feed_id: FEED,
            name: "ETH/USD".into(),
            decimals,
        }
        .data(),
    }
}

struct TestNode {
    key: Keypair,
    tokens: Pubkey,
}

fn register_ix(node: &TestNode, amount: u64) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::Register {
            signer: node.key.pubkey(),
            config: config_pda(),
            node: node_pda(&node.key.pubkey()),
            node_tokens: node.tokens,
            stake_vault: vault_pda(),
            token_program: spl_token::ID,
            system_program: SYSTEM,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::Register { amount }.data(),
    }
}

fn funded_node(env: &mut Env, tokens: u64) -> TestNode {
    let key = Keypair::new();
    env.svm.airdrop(&key.pubkey(), 10_000_000_000).unwrap();
    let tokens = token_account(env, &key.pubkey(), tokens);
    TestNode { key, tokens }
}

fn registered_node(env: &mut Env) -> TestNode {
    let node = funded_node(env, 10 * MIN_STAKE);
    let key = node.key.insecure_clone();
    send(&mut env.svm, &[register_ix(&node, MIN_STAKE)], &key, &[]).expect("register");
    node
}

fn config(env: &Env) -> Config {
    Config::try_deserialize(&mut env.svm.get_account(&config_pda()).unwrap().data.as_slice())
        .unwrap()
}
fn node_state(env: &Env, node: &Pubkey) -> Node {
    Node::try_deserialize(
        &mut env
            .svm
            .get_account(&node_pda(node))
            .unwrap()
            .data
            .as_slice(),
    )
    .unwrap()
}
fn round_state(env: &Env, id: u64) -> Round {
    Round::try_deserialize(&mut env.svm.get_account(&round_pda(id)).unwrap().data.as_slice())
        .unwrap()
}
fn feed_state(env: &Env) -> Feed {
    Feed::try_deserialize(&mut env.svm.get_account(&feed_pda()).unwrap().data.as_slice()).unwrap()
}

fn open_round(env: &mut Env) -> Outcome {
    let cfg = config(env);
    let feed = feed_state(env);
    let ix = Instruction {
        program_id: PROGRAM,
        accounts: accounts::OpenRound {
            payer: env.admin.pubkey(),
            config: config_pda(),
            feed: feed_pda(),
            current_round: round_pda(feed.current_round_id),
            round: round_pda(cfg.next_round_id),
            system_program: SYSTEM,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::OpenRound {}.data(),
    };
    let admin = env.admin.insecure_clone();
    send(&mut env.svm, &[ix], &admin, &[])
}

// The Ed25519 program instruction as Solana's builder lays it out.
fn ed25519_ix(signer: &Keypair, message: &[u8]) -> Instruction {
    let signature = signer.sign_message(message);
    let pubkey_offset: u16 = 16;
    let sig_offset = pubkey_offset + 32;
    let msg_offset = sig_offset + 64;
    let mut data = vec![1u8, 0];
    for v in [
        sig_offset,
        u16::MAX,
        pubkey_offset,
        u16::MAX,
        msg_offset,
        message.len() as u16,
        u16::MAX,
    ] {
        data.extend_from_slice(&v.to_le_bytes());
    }
    data.extend_from_slice(signer.pubkey().as_ref());
    data.extend_from_slice(signature.as_ref());
    data.extend_from_slice(message);
    Instruction {
        program_id: ED25519_PROGRAM,
        accounts: vec![],
        data,
    }
}

fn message_for(node: &Pubkey, round: u64, value: u128, nonce: u64) -> Vec<u8> {
    submission_message(&PROGRAM, CHAIN_ID, round, &FEED, value, node, nonce).to_vec()
}

fn submit_ix(node: &Pubkey, round: u64, value: u128, nonce: u64) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::Submit {
            signer: *node,
            config: config_pda(),
            node: node_pda(node),
            round: round_pda(round),
            submission: submission_pda(round, node),
            instructions: INSTRUCTIONS_SYSVAR,
            system_program: SYSTEM,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::Submit { value, nonce }.data(),
    }
}

fn submit(env: &mut Env, node: &TestNode, round: u64, value: u128) -> Outcome {
    let nonce = node_state(env, &node.key.pubkey()).nonce;
    let key = node.key.insecure_clone();
    let ixs = [
        ed25519_ix(&key, &message_for(&key.pubkey(), round, value, nonce)),
        submit_ix(&key.pubkey(), round, value, nonce),
    ];
    send(&mut env.svm, &ixs, &key, &[])
}

fn settle(env: &mut Env, round: u64) -> Outcome {
    let ix = Instruction {
        program_id: PROGRAM,
        accounts: accounts::SettleRound {
            config: config_pda(),
            round: round_pda(round),
            feed: feed_pda(),
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::SettleRound {}.data(),
    };
    let admin = env.admin.insecure_clone();
    send(&mut env.svm, &[ix], &admin, &[])
}

fn manage(env: &mut Env, signer: &Keypair, data: Vec<u8>) -> Outcome {
    let ix = Instruction {
        program_id: PROGRAM,
        accounts: accounts::Manage {
            signer: signer.pubkey(),
            config: config_pda(),
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data,
    };
    let admin = env.admin.insecure_clone();
    send(&mut env.svm, &[ix], &admin, &[signer])
}

fn slash_ix(slasher: &Pubkey, node: &Pubkey, round: u64, amount: u64) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::Slash {
            slasher: *slasher,
            config: config_pda(),
            node: node_pda(node),
            slash_record: slash_pda(round, node),
            system_program: SYSTEM,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::Slash {
            round_id: round,
            amount,
            reason: *b"MISSED_ROUND\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0",
        }
        .data(),
    }
}

fn node_action(signer: &Pubkey, data: Vec<u8>) -> Instruction {
    Instruction {
        program_id: PROGRAM,
        accounts: accounts::NodeAction {
            signer: *signer,
            config: config_pda(),
            node: node_pda(signer),
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data,
    }
}

fn get_value(env: &mut Env, max_staleness: i64) -> Result<ValueView, Vec<String>> {
    let ix = Instruction {
        program_id: PROGRAM,
        accounts: accounts::GetValue { feed: feed_pda() }.to_account_metas(None),
        data: instruction::GetValue { max_staleness }.data(),
    };
    env.svm.expire_blockhash();
    let admin = env.admin.insecure_clone();
    let tx = Transaction::new(
        &[&admin],
        Message::new(&[ix], Some(&admin.pubkey())),
        env.svm.latest_blockhash(),
    );
    let meta = env.svm.send_transaction(tx).map_err(|f| f.meta.logs)?;
    Ok(ValueView::deserialize(&mut meta.return_data.data.as_slice()).unwrap())
}

fn open_with_nodes(env: &mut Env, count: usize) -> Vec<TestNode> {
    let nodes: Vec<TestNode> = (0..count).map(|_| registered_node(env)).collect();
    open_round(env).expect("open round");
    nodes
}

// --- initialization ---

#[test]
fn only_the_upgrade_authority_initializes() {
    let mut env = uninitialized();
    let attacker = Keypair::new();
    env.svm.airdrop(&attacker.pubkey(), 10_000_000_000).unwrap();
    let a = args(&env);
    refused(
        send(
            &mut env.svm,
            &[initialize_ix(&attacker.pubkey(), env.mint, a)],
            &attacker,
            &[],
        ),
        "NotUpgradeAuthority",
    );
}

#[test]
fn a_token_2022_stake_mint_is_refused() {
    let mut env = uninitialized();
    let mint = mint_account(&mut env.svm, TOKEN_2022);
    let admin = env.admin.insecure_clone();
    let a = args(&env);
    refused(
        send(
            &mut env.svm,
            &[initialize_ix(&admin.pubkey(), mint, a)],
            &admin,
            &[],
        ),
        "AccountOwnedByWrongProgram",
    );
}

#[test]
fn parameters_outside_their_bounds_are_refused() {
    let cases: Vec<ArgsCase> = vec![
        (
            "33 nodes",
            Box::new(|a| a.max_nodes = 33),
            "InvalidParameter",
        ),
        (
            "zero nodes",
            Box::new(|a| a.max_nodes = 0),
            "InvalidParameter",
        ),
        (
            "slash over 100%",
            Box::new(|a| a.max_slash_bps = 10_001),
            "InvalidBps",
        ),
        ("zero quorum", Box::new(|a| a.quorum_bps = 0), "InvalidBps"),
        (
            "short unbonding",
            Box::new(|a| a.unbonding_period = 60),
            "UnbondingPeriodTooShort",
        ),
        (
            "floor above minimum",
            Box::new(|a| a.min_stake_floor = MIN_STAKE + 1),
            "StakeBelowMinimum",
        ),
        (
            "zero slasher",
            Box::new(|a| a.slasher = Pubkey::default()),
            "ZeroAddress",
        ),
    ];
    for (name, mutate, reason) in cases {
        let mut env = uninitialized();
        let mut a = args(&env);
        mutate(&mut a);
        let admin = env.admin.insecure_clone();
        let result = send(
            &mut env.svm,
            &[initialize_ix(&admin.pubkey(), env.mint, a)],
            &admin,
            &[],
        );
        assert!(result.is_err(), "{name} accepted");
        refused(result, reason);
    }
}

// --- staking ---

#[test]
fn registration_needs_the_minimum_and_room() {
    let mut env = new_env();
    let poor = funded_node(&mut env, MIN_STAKE);
    let key = poor.key.insecure_clone();
    refused(
        send(
            &mut env.svm,
            &[register_ix(&poor, MIN_STAKE - 1)],
            &key,
            &[],
        ),
        "StakeBelowMinimum",
    );

    for _ in 0..4 {
        registered_node(&mut env);
    }
    let fifth = funded_node(&mut env, MIN_STAKE);
    let key = fifth.key.insecure_clone();
    refused(
        send(&mut env.svm, &[register_ix(&fifth, MIN_STAKE)], &key, &[]),
        "NodeSetFull",
    );

    let cfg = config(&env);
    assert_eq!((cfg.active_node_count, cfg.node_set_version), (4, 4));
    assert_eq!(balance(&env.svm, &vault_pda()), 4 * MIN_STAKE);
}

#[test]
fn unstaking_deactivates_waits_out_unbonding_and_returns_the_stake() {
    let mut env = new_env();
    let node = registered_node(&mut env);
    let key = node.key.insecure_clone();

    send(
        &mut env.svm,
        &[node_action(
            &key.pubkey(),
            instruction::RequestUnstake { amount: MIN_STAKE }.data(),
        )],
        &key,
        &[],
    )
    .unwrap();
    assert!(!node_state(&env, &key.pubkey()).active);
    assert_eq!(config(&env).active_node_count, 0);

    let complete = Instruction {
        program_id: PROGRAM,
        accounts: accounts::CompleteUnstake {
            signer: key.pubkey(),
            config: config_pda(),
            node: node_pda(&key.pubkey()),
            node_tokens: node.tokens,
            stake_vault: vault_pda(),
            token_program: spl_token::ID,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::CompleteUnstake {}.data(),
    };
    refused(
        send(&mut env.svm, std::slice::from_ref(&complete), &key, &[]),
        "UnbondingNotElapsed",
    );

    warp(&mut env.svm, 24 * 60 * 60);
    send(&mut env.svm, &[complete], &key, &[]).unwrap();
    assert_eq!(balance(&env.svm, &node.tokens), 10 * MIN_STAKE);
    assert_eq!(node_state(&env, &key.pubkey()).stake, 0);
}

#[test]
fn cancelling_an_unstake_reactivates_only_when_there_is_room() {
    let mut env = new_env();
    let leaving = registered_node(&mut env);
    let key = leaving.key.insecure_clone();
    send(
        &mut env.svm,
        &[node_action(
            &key.pubkey(),
            instruction::RequestUnstake { amount: 1 }.data(),
        )],
        &key,
        &[],
    )
    .unwrap();

    for _ in 0..4 {
        registered_node(&mut env);
    }
    send(
        &mut env.svm,
        &[node_action(
            &key.pubkey(),
            instruction::CancelUnstake {}.data(),
        )],
        &key,
        &[],
    )
    .unwrap();
    assert!(
        !node_state(&env, &key.pubkey()).active,
        "reactivated into a full node set"
    );
    assert_eq!(config(&env).active_node_count, 4);
}

// --- rounds ---

#[test]
fn a_round_settles_at_the_median_and_is_read_with_a_staleness_bound() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 4);

    refused(settle(&mut env, 1), "RoundStillOpen");
    for (node, value) in nodes.iter().zip([100u128, 300, 200]) {
        submit(&mut env, node, 1, value).expect("submit");
    }
    assert_eq!(round_state(&env, 1).state, ROUND_QUORUM_MET);

    settle(&mut env, 1).expect("settle");
    let round = round_state(&env, 1);
    assert_eq!((round.state, round.aggregated_value), (ROUND_SETTLED, 200));

    let view = get_value(&mut env, 10).expect("fresh value");
    assert_eq!((view.value, view.round_id), (200, 1));

    warp(&mut env.svm, 11);
    refused(get_value(&mut env, 10).map(|_| vec![]), "StaleValue");
}

#[test]
fn a_round_without_quorum_fails_only_after_its_deadline() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 4);
    submit(&mut env, &nodes[0], 1, 100).unwrap();

    refused(settle(&mut env, 1), "RoundStillOpen");
    warp(&mut env.svm, ROUND_DURATION + 1);
    settle(&mut env, 1).expect("fail the round");
    assert_eq!(round_state(&env, 1).state, ROUND_FAILED);
    refused(get_value(&mut env, 1_000).map(|_| vec![]), "NoSettledRound");
}

#[test]
fn a_second_round_cannot_open_while_one_is_live() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 2);
    refused(open_round(&mut env), "RoundAlreadyOpen");

    for (node, value) in nodes.iter().zip([1u128, 2]) {
        submit(&mut env, node, 1, value).unwrap();
    }
    settle(&mut env, 1).unwrap();
    open_round(&mut env).expect("opens once the previous round is final");
}

#[test]
fn a_round_does_not_open_without_enough_nodes() {
    let mut env = new_env();
    registered_node(&mut env);
    refused(open_round(&mut env), "NotEnoughEligibleNodes");
}

#[test]
fn submissions_are_refused_after_the_deadline_twice_or_by_a_latecomer() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 3);

    submit(&mut env, &nodes[0], 1, 100).unwrap();
    // The submission account already exists; the nonce has moved on, so resend with the old nonce.
    let key = nodes[0].key.insecure_clone();
    let replay = [
        ed25519_ix(&key, &message_for(&key.pubkey(), 1, 100, 1)),
        submit_ix(&key.pubkey(), 1, 100, 1),
    ];
    refused(send(&mut env.svm, &replay, &key, &[]), "already in use");

    let latecomer = registered_node(&mut env);
    refused(submit(&mut env, &latecomer, 1, 100), "NotEligible");

    warp(&mut env.svm, ROUND_DURATION + 1);
    refused(submit(&mut env, &nodes[1], 1, 100), "RoundNotOpen");
}

#[test]
fn a_node_that_left_and_rejoined_is_not_eligible_for_a_round_opened_before() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 3);
    let key = nodes[0].key.insecure_clone();
    send(
        &mut env.svm,
        &[node_action(
            &key.pubkey(),
            instruction::RequestUnstake { amount: 1 }.data(),
        )],
        &key,
        &[],
    )
    .unwrap();
    send(
        &mut env.svm,
        &[node_action(
            &key.pubkey(),
            instruction::CancelUnstake {}.data(),
        )],
        &key,
        &[],
    )
    .unwrap();
    assert!(node_state(&env, &key.pubkey()).active);
    refused(submit(&mut env, &nodes[0], 1, 100), "NotEligible");
}

#[test]
fn a_wrong_nonce_is_refused() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 2);
    let key = nodes[0].key.insecure_clone();
    let ixs = [
        ed25519_ix(&key, &message_for(&key.pubkey(), 1, 5, 7)),
        submit_ix(&key.pubkey(), 1, 5, 7),
    ];
    refused(send(&mut env.svm, &ixs, &key, &[]), "InvalidNonce");
}

#[test]
fn pausing_stops_opening_and_submitting() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 2);
    let pauser = env.pauser.insecure_clone();
    manage(
        &mut env,
        &pauser,
        instruction::SetPaused { paused: true }.data(),
    )
    .unwrap();
    refused(submit(&mut env, &nodes[0], 1, 1), "Paused");
    refused(open_round(&mut env), "Paused");
}

// Topping up a node that fell under the floor reactivates it, but never into a full set.
#[test]
fn staking_back_above_the_minimum_reactivates_only_when_there_is_room() {
    let mut env = new_env();
    let node = registered_node(&mut env);
    let key = node.key.insecure_clone();
    let slasher = env.slasher.insecure_clone();
    send(
        &mut env.svm,
        &[slash_ix(&slasher.pubkey(), &key.pubkey(), 1, 60)],
        &slasher,
        &[],
    )
    .unwrap();
    assert!(!node_state(&env, &key.pubkey()).active);

    for _ in 0..4 {
        registered_node(&mut env);
    }
    let top_up = Instruction {
        program_id: PROGRAM,
        accounts: accounts::StakeMore {
            signer: key.pubkey(),
            config: config_pda(),
            node: node_pda(&key.pubkey()),
            node_tokens: node.tokens,
            stake_vault: vault_pda(),
            token_program: spl_token::ID,
            event_authority: event_authority(),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::Stake { amount: 100 }.data(),
    };
    send(&mut env.svm, &[top_up], &key, &[]).expect("stake");
    let n = node_state(&env, &key.pubkey());
    assert_eq!(
        (n.stake, n.active),
        (1_040, false),
        "reactivated into a full node set"
    );
    assert_eq!(config(&env).active_node_count, 4);
}

// --- the attestation ---

#[test]
fn a_submission_without_a_verified_attestation_is_refused() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 2);
    let key = nodes[0].key.insecure_clone();
    let node = key.pubkey();

    refused(
        send(&mut env.svm, &[submit_ix(&node, 1, 100, 0)], &key, &[]),
        "InvalidSignatureInstruction",
    );

    let stranger = Keypair::new();
    let signed_by_stranger = [
        ed25519_ix(&stranger, &message_for(&node, 1, 100, 0)),
        submit_ix(&node, 1, 100, 0),
    ];
    refused(
        send(&mut env.svm, &signed_by_stranger, &key, &[]),
        "InvalidSignature",
    );

    let different_value = [
        ed25519_ix(&key, &message_for(&node, 1, 999, 0)),
        submit_ix(&node, 1, 100, 0),
    ];
    refused(
        send(&mut env.svm, &different_value, &key, &[]),
        "InvalidSignature",
    );

    let other_chain = submission_message(&PROGRAM, CHAIN_ID + 1, 1, &FEED, 100, &node, 0).to_vec();
    refused(
        send(
            &mut env.svm,
            &[ed25519_ix(&key, &other_chain), submit_ix(&node, 1, 100, 0)],
            &key,
            &[],
        ),
        "InvalidSignature",
    );

    let other_program =
        submission_message(&Pubkey::new_unique(), CHAIN_ID, 1, &FEED, 100, &node, 0).to_vec();
    refused(
        send(
            &mut env.svm,
            &[
                ed25519_ix(&key, &other_program),
                submit_ix(&node, 1, 100, 0),
            ],
            &key,
            &[],
        ),
        "InvalidSignature",
    );
}

// Verified, but not immediately before: the check reads exactly one instruction back.
#[test]
fn an_attestation_that_is_not_the_preceding_instruction_is_refused() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 2);
    let key = nodes[0].key.insecure_clone();
    let node = key.pubkey();
    let memo = Instruction {
        program_id: SYSTEM,
        accounts: vec![AccountMeta::new(node, true), AccountMeta::new(node, false)],
        data: {
            let mut d = 2u32.to_le_bytes().to_vec();
            d.extend_from_slice(&0u64.to_le_bytes());
            d
        },
    };
    let ixs = [
        ed25519_ix(&key, &message_for(&node, 1, 100, 0)),
        memo,
        submit_ix(&node, 1, 100, 0),
    ];
    refused(
        send(&mut env.svm, &ixs, &key, &[]),
        "InvalidSignatureInstruction",
    );
}

// The account the check reads must be the real instructions sysvar, not an account shaped like one.
#[test]
fn a_substituted_instructions_account_is_refused() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 2);
    let key = nodes[0].key.insecure_clone();
    let node = key.pubkey();

    let fake = Pubkey::new_unique();
    env.svm
        .set_account(fake, env.svm.get_account(&config_pda()).unwrap())
        .unwrap();
    let mut submit = submit_ix(&node, 1, 100, 0);
    for meta in submit.accounts.iter_mut() {
        if meta.pubkey == INSTRUCTIONS_SYSVAR {
            meta.pubkey = fake;
        }
    }
    let ixs = [ed25519_ix(&key, &message_for(&node, 1, 100, 0)), submit];
    refused(send(&mut env.svm, &ixs, &key, &[]), "ConstraintAddress");
}

// A valid Ed25519 instruction whose offsets read from another instruction's data. The runtime accepts
// it; the program must not, because it would be checking bytes the runtime did not verify here.
#[test]
fn an_attestation_reading_another_instructions_data_is_refused() {
    let mut env = new_env();
    let nodes = open_with_nodes(&mut env, 2);
    let key = nodes[0].key.insecure_clone();
    let node = key.pubkey();

    let mut verify = ed25519_ix(&key, &message_for(&node, 1, 100, 0));
    for field in [4usize, 8, 14] {
        verify.data[field..field + 2].copy_from_slice(&0u16.to_le_bytes());
    }
    let ixs = [verify, submit_ix(&node, 1, 100, 0)];
    refused(
        send(&mut env.svm, &ixs, &key, &[]),
        "InvalidSignatureInstruction",
    );
}

// --- slashing ---

#[test]
fn slashing_is_capped_once_per_round_and_deactivates_under_the_floor() {
    let mut env = new_env();
    let node = registered_node(&mut env);
    let target = node.key.pubkey();
    let slasher = env.slasher.insecure_clone();

    refused(
        send(
            &mut env.svm,
            &[slash_ix(&slasher.pubkey(), &target, 1, 101)],
            &slasher,
            &[],
        ),
        "SlashExceedsCap",
    );

    let stranger = Keypair::new();
    env.svm.airdrop(&stranger.pubkey(), 1_000_000_000).unwrap();
    refused(
        send(
            &mut env.svm,
            &[slash_ix(&stranger.pubkey(), &target, 1, 10)],
            &stranger,
            &[],
        ),
        "Unauthorized",
    );

    send(
        &mut env.svm,
        &[slash_ix(&slasher.pubkey(), &target, 1, 60)],
        &slasher,
        &[],
    )
    .expect("slash");
    let n = node_state(&env, &target);
    assert_eq!(
        (n.stake, n.slashed_total, n.active),
        (940, 60, false),
        "940 is under the 950 floor"
    );
    assert_eq!(config(&env).active_node_count, 0);

    refused(
        send(
            &mut env.svm,
            &[slash_ix(&slasher.pubkey(), &target, 1, 10)],
            &slasher,
            &[],
        ),
        "already in use",
    );
    send(
        &mut env.svm,
        &[slash_ix(&slasher.pubkey(), &target, 2, 10)],
        &slasher,
        &[],
    )
    .expect("another round");
}

// --- roles ---

#[test]
fn each_setter_refuses_the_wrong_role() {
    let mut env = new_env();
    let (admin, manager, pauser, slasher) = (
        env.admin.insecure_clone(),
        env.manager.insecure_clone(),
        env.pauser.insecure_clone(),
        env.slasher.insecure_clone(),
    );
    let cases: Vec<(&str, Vec<u8>, &Keypair)> = vec![
        (
            "set_minimum_stake",
            instruction::SetMinimumStake { value: 2_000 }.data(),
            &admin,
        ),
        (
            "set_min_stake_floor",
            instruction::SetMinStakeFloor { value: 900 }.data(),
            &admin,
        ),
        (
            "set_unbonding_period",
            instruction::SetUnbondingPeriod { value: 2 * 86_400 }.data(),
            &admin,
        ),
        (
            "set_max_nodes",
            instruction::SetMaxNodes { value: 8 }.data(),
            &admin,
        ),
        (
            "set_round_duration",
            instruction::SetRoundDuration { value: 120 }.data(),
            &manager,
        ),
        (
            "set_quorum_bps",
            instruction::SetQuorumBps { value: 7_000 }.data(),
            &manager,
        ),
        (
            "set_min_quorum_nodes",
            instruction::SetMinQuorumNodes { value: 3 }.data(),
            &manager,
        ),
        (
            "set_paused",
            instruction::SetPaused { paused: true }.data(),
            &pauser,
        ),
    ];
    for (name, data, right) in cases {
        for wrong in [&admin, &manager, &pauser, &slasher] {
            if wrong.pubkey() == right.pubkey() {
                continue;
            }
            let result = manage(&mut env, wrong, data.clone());
            assert!(result.is_err(), "{name} accepted from the wrong role");
            refused(result, "Unauthorized");
        }
        manage(&mut env, right, data)
            .unwrap_or_else(|l| panic!("{name} refused its role:\n{}", l.join("\n")));
    }
    refused(
        manage(
            &mut env,
            &admin,
            instruction::SetMaxNodes { value: 33 }.data(),
        ),
        "InvalidParameter",
    );
    let manager = env.manager.insecure_clone();
    refused(
        manage(
            &mut env,
            &manager,
            instruction::SetRoundDuration { value: 0 }.data(),
        ),
        "ZeroValue",
    );
    refused(
        manage(
            &mut env,
            &manager,
            instruction::SetQuorumBps { value: 10_001 }.data(),
        ),
        "InvalidBps",
    );
}

#[test]
fn the_admin_changes_hands_only_when_the_proposed_key_accepts() {
    let mut env = new_env();
    let (old, new) = (env.admin.insecure_clone(), Keypair::new());
    refused(
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
    refused(
        manage(
            &mut env,
            &Keypair::new(),
            instruction::AcceptAdmin {}.data(),
        ),
        "Unauthorized",
    );
    manage(&mut env, &new, instruction::AcceptAdmin {}.data()).unwrap();
    assert_eq!(config(&env).admin, new.pubkey());
    refused(
        manage(&mut env, &old, instruction::SetMaxNodes { value: 5 }.data()),
        "Unauthorized",
    );
}
