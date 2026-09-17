use aegis_governance_receiver::{
    accounts, instruction, message::accounts_hash, state::*, InitializeArgs, WORMHOLE_CORE_BRIDGE,
    WORMHOLE_VERIFY_VAA_SHIM,
};
use anchor_lang::{
    prelude::{AccountMeta, Pubkey},
    AccountDeserialize, InstructionData, ToAccountMetas,
};
use anchor_spl::token::spl_token;
use k256::ecdsa::SigningKey;
use litesvm::LiteSVM;
use solana_account::Account;
use solana_clock::Clock;
use solana_keypair::Keypair;
use solana_message::{Instruction, Message};
use solana_program_pack::Pack;
use solana_signer::Signer;
use solana_transaction::Transaction;

const PROGRAM: Pubkey = aegis_governance_receiver::ID;
const VAULT: Pubkey = aegis_vault::ID;
const SYSTEM: Pubkey = anchor_lang::system_program::ID;
const BPF_LOADER_UPGRADEABLE: Pubkey =
    anchor_lang::pubkey!("BPFLoaderUpgradeab1e11111111111111111111111");
const SYSVAR_CLOCK: Pubkey = anchor_lang::pubkey!("SysvarC1ock11111111111111111111111111111111");
const SYSVAR_RENT: Pubkey = anchor_lang::pubkey!("SysvarRent111111111111111111111111111111111");

// Wormhole's published development guardian. Safe only because no real guardian set contains it.
const GUARDIAN_KEY: &str = "cfb12303a19cde580bb4dd771639b0d26bc68353645571a8cff516ab2ee113a0";
const LOCALNET_CHAIN_ID: u64 = (1 << 62) + 3;
const ARBITRUM_WORMHOLE_CHAIN: u16 = 23;
const DELAY: i64 = 3_600;
const WINDOW: i64 = 86_400;

fn dispatcher() -> [u8; 32] {
    let mut out = [0u8; 32];
    out[12..].copy_from_slice(&[0xd1; 20]);
    out
}

struct Env {
    svm: LiteSVM,
    admin: Keypair,
    guardian: Keypair,
    next_sequence: u64,
}

fn load(svm: &mut LiteSVM, id: Pubkey, path: &str) {
    let bytes =
        std::fs::read(path).unwrap_or_else(|e| panic!("{path}: {e}; run `anchor build` first"));
    svm.add_program(id, &bytes).unwrap();
}

fn program_data(program: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[program.as_ref()], &BPF_LOADER_UPGRADEABLE).0
}

fn set_upgrade_authority(svm: &mut LiteSVM, program: &Pubkey, authority: &Pubkey) {
    let address = program_data(program);
    let mut account = svm.get_account(&address).unwrap();
    account.data[12] = 1;
    account.data[13..45].copy_from_slice(authority.as_ref());
    svm.set_account(address, account).unwrap();
}

fn pda(seeds: &[&[u8]], program: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(seeds, program).0
}

fn config_pda() -> Pubkey {
    pda(&[CONFIG_SEED], &PROGRAM)
}

fn authority() -> Pubkey {
    pda(&[AUTHORITY_SEED], &PROGRAM)
}

fn event_authority(program: &Pubkey) -> Pubkey {
    pda(&[b"__event_authority"], program)
}

fn message_pda(emitter_chain: u16, sequence: u64) -> Pubkey {
    pda(
        &[
            MESSAGE_SEED,
            &emitter_chain.to_be_bytes(),
            &sequence.to_be_bytes(),
        ],
        &PROGRAM,
    )
}

fn guardian_set() -> (Pubkey, u8) {
    Pubkey::find_program_address(
        &[b"GuardianSet", &0u32.to_be_bytes()],
        &WORMHOLE_CORE_BRIDGE,
    )
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
    // LiteSVM does not enforce the network's packet limit; a relayer would meet it.
    let size = 1 + 64 * tx.signatures.len() + tx.message.serialize().len();
    assert!(
        size <= 1232,
        "transaction is {size} bytes, over Solana's 1232"
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

fn account<T: AccountDeserialize>(svm: &LiteSVM, address: &Pubkey) -> T {
    T::try_deserialize(&mut svm.get_account(address).unwrap().data.as_slice()).unwrap()
}

fn warp(svm: &mut LiteSVM, seconds: i64) {
    let mut clock: Clock = svm.get_sysvar();
    clock.unix_timestamp += seconds;
    svm.set_sysvar(&clock);
}

fn discriminator(name: &str) -> Vec<u8> {
    aegis_governance_receiver::anchor_discriminator(name).to_vec()
}

// --- Wormhole ---

fn vaa_body(emitter_chain: u16, emitter: [u8; 32], sequence: u64, payload: &[u8]) -> Vec<u8> {
    let mut body = 1_700_000_000u32.to_be_bytes().to_vec();
    body.extend_from_slice(&0u32.to_be_bytes());
    body.extend_from_slice(&emitter_chain.to_be_bytes());
    body.extend_from_slice(&emitter);
    body.extend_from_slice(&sequence.to_be_bytes());
    body.push(1);
    body.extend_from_slice(payload);
    body
}

fn guardian_signature(body: &[u8], key_hex: &str) -> [u8; 66] {
    let key = SigningKey::from_slice(&hex(key_hex)).unwrap();
    let digest = aegis_governance_receiver::message::vaa_digest(body);
    let (signature, recovery) = key.sign_prehash_recoverable(&digest).unwrap();
    let mut out = [0u8; 66];
    out[1..65].copy_from_slice(&signature.to_bytes());
    out[65] = recovery.to_byte();
    out
}

fn guardian_address(key_hex: &str) -> [u8; 20] {
    let key = SigningKey::from_slice(&hex(key_hex)).unwrap();
    let point = key.verifying_key().to_encoded_point(false);
    solana_keccak_hasher::hash(&point.as_bytes()[1..]).to_bytes()[12..]
        .try_into()
        .unwrap()
}

fn hex(s: &str) -> Vec<u8> {
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).unwrap())
        .collect()
}

fn initialize_core_bridge(svm: &mut LiteSVM, payer: &Keypair) {
    let mut data = vec![0u8];
    data.extend_from_slice(&86_400u32.to_le_bytes());
    data.extend_from_slice(&0u64.to_le_bytes());
    data.extend_from_slice(&1u32.to_le_bytes());
    data.extend_from_slice(&guardian_address(GUARDIAN_KEY));
    let ix = Instruction {
        program_id: WORMHOLE_CORE_BRIDGE,
        accounts: vec![
            AccountMeta::new(pda(&[b"Bridge"], &WORMHOLE_CORE_BRIDGE), false),
            AccountMeta::new(guardian_set().0, false),
            AccountMeta::new(pda(&[b"fee_collector"], &WORMHOLE_CORE_BRIDGE), false),
            AccountMeta::new(payer.pubkey(), true),
            AccountMeta::new_readonly(SYSVAR_CLOCK, false),
            AccountMeta::new_readonly(SYSVAR_RENT, false),
            AccountMeta::new_readonly(SYSTEM, false),
        ],
        data,
    };
    send(svm, &[ix], payer, &[]).expect("initialize the core bridge");
}

#[test]
fn the_test_guardian_is_wormholes_development_key() {
    assert_eq!(
        guardian_address(GUARDIAN_KEY).to_vec(),
        hex("befa429d57cd18b7f8a4d91a2da9ab4af05d0fbe")
    );
}

// --- setup ---

impl Env {
    fn new() -> Env {
        let mut svm = LiteSVM::new();
        let root = concat!(env!("CARGO_MANIFEST_DIR"), "/../..");
        load(
            &mut svm,
            WORMHOLE_CORE_BRIDGE,
            &format!("{root}/external/wormhole_core_bridge.so"),
        );
        load(
            &mut svm,
            WORMHOLE_VERIFY_VAA_SHIM,
            &format!("{root}/external/wormhole_verify_vaa_shim.so"),
        );
        load(
            &mut svm,
            PROGRAM,
            &format!("{root}/target/deploy/aegis_governance_receiver.so"),
        );
        load(
            &mut svm,
            VAULT,
            &format!("{root}/target/deploy/aegis_vault.so"),
        );

        let admin = Keypair::new();
        svm.airdrop(&admin.pubkey(), 1_000_000_000_000).unwrap();
        set_upgrade_authority(&mut svm, &PROGRAM, &admin.pubkey());
        set_upgrade_authority(&mut svm, &VAULT, &admin.pubkey());
        warp(&mut svm, 1_700_000_000);
        initialize_core_bridge(&mut svm, &admin);

        let mut env = Env {
            svm,
            admin,
            guardian: Keypair::new(),
            next_sequence: 1,
        };
        env.svm
            .airdrop(&env.guardian.pubkey(), 1_000_000_000)
            .unwrap();
        let ix = env.initialize_ix(&env.admin.pubkey());
        env.as_admin(&[ix]).expect("initialize the receiver");
        for program in [PROGRAM, VAULT, spl_token::ID, SYSTEM] {
            let ix = env.set_allowed_ix(&env.admin.pubkey(), program, true);
            env.as_admin(&[ix]).expect("allow a program");
        }
        env
    }

    fn as_admin(&mut self, ixs: &[Instruction]) -> Result<Vec<String>, Vec<String>> {
        let admin = self.admin.insecure_clone();
        send(&mut self.svm, ixs, &admin, &[])
    }

    fn as_signer(
        &mut self,
        signer: &Keypair,
        ixs: &[Instruction],
    ) -> Result<Vec<String>, Vec<String>> {
        send(&mut self.svm, ixs, signer, &[])
    }

    fn config(&self) -> Config {
        account(&self.svm, &config_pda())
    }

    fn initialize_ix(&self, signer: &Pubkey) -> Instruction {
        Instruction {
            program_id: PROGRAM,
            accounts: accounts::Initialize {
                admin: *signer,
                config: config_pda(),
                authority: authority(),
                this_program: PROGRAM,
                program_data: program_data(&PROGRAM),
                system_program: SYSTEM,
                event_authority: event_authority(&PROGRAM),
                program: PROGRAM,
            }
            .to_account_metas(None),
            data: instruction::Initialize {
                args: InitializeArgs {
                    chain_id: LOCALNET_CHAIN_ID,
                    emitter_chain: ARBITRUM_WORMHOLE_CHAIN,
                    emitter_address: dispatcher(),
                    guardian: self.guardian.pubkey(),
                    delay: DELAY,
                    window: WINDOW,
                },
            }
            .data(),
        }
    }

    fn govern_ix(controller: &Pubkey, data: Vec<u8>) -> Instruction {
        Instruction {
            program_id: PROGRAM,
            accounts: accounts::Govern {
                controller: *controller,
                config: config_pda(),
                event_authority: event_authority(&PROGRAM),
                program: PROGRAM,
            }
            .to_account_metas(None),
            data,
        }
    }

    fn set_allowed_ix(&self, controller: &Pubkey, target: Pubkey, allowed: bool) -> Instruction {
        Instruction {
            program_id: PROGRAM,
            accounts: accounts::SetAllowed {
                controller: *controller,
                config: config_pda(),
                allowed: pda(&[ALLOWED_SEED, target.as_ref()], &PROGRAM),
                system_program: SYSTEM,
                event_authority: event_authority(&PROGRAM),
                program: PROGRAM,
            }
            .to_account_metas(None),
            data: instruction::SetAllowed { target, allowed }.data(),
        }
    }

    fn register_treasury(
        &mut self,
        mint: Pubkey,
        watched: Pubkey,
        per_message_cap: u64,
        rolling_cap: u64,
    ) -> Result<Vec<String>, Vec<String>> {
        let ix = Instruction {
            program_id: PROGRAM,
            accounts: accounts::RegisterTreasury {
                controller: self.admin.pubkey(),
                config: config_pda(),
                treasury: pda(&[TREASURY_SEED, mint.as_ref()], &PROGRAM),
                window: pda(&[WINDOW_SEED, mint.as_ref()], &PROGRAM),
                account: watched,
                authority: authority(),
                system_program: SYSTEM,
                event_authority: event_authority(&PROGRAM),
                program: PROGRAM,
            }
            .to_account_metas(None),
            data: instruction::RegisterTreasury {
                mint,
                per_message_cap,
                rolling_cap,
            }
            .data(),
        };
        self.as_admin(&[ix])
    }

    fn finish_bootstrap(&mut self) {
        let ix = Env::govern_ix(&self.admin.pubkey(), instruction::FinishBootstrap {}.data());
        self.as_admin(&[ix]).expect("finish bootstrap");
    }

    // --- relaying ---

    /// Signs `body` with `key`, posts the signatures to the shim, and receives the message.
    fn relay(&mut self, body: &[u8], signing_key: &str) -> Result<Vec<String>, Vec<String>> {
        let signatures = Keypair::new();
        let mut data = discriminator("post_signatures");
        data.extend_from_slice(&0u32.to_le_bytes());
        data.push(1);
        data.extend_from_slice(&1u32.to_le_bytes());
        data.extend_from_slice(&guardian_signature(body, signing_key));
        let post = Instruction {
            program_id: WORMHOLE_VERIFY_VAA_SHIM,
            accounts: vec![
                AccountMeta::new(self.admin.pubkey(), true),
                AccountMeta::new(signatures.pubkey(), true),
                AccountMeta::new_readonly(SYSTEM, false),
            ],
            data,
        };
        let admin = self.admin.insecure_clone();
        send(&mut self.svm, &[post], &admin, &[&signatures]).expect("post signatures");

        let emitter_chain = u16::from_be_bytes([body[8], body[9]]);
        let sequence = u64::from_be_bytes(body[42..50].try_into().unwrap());
        let (guardian_set, bump) = guardian_set();
        let receive = Instruction {
            program_id: PROGRAM,
            accounts: accounts::ReceiveMessage {
                payer: self.admin.pubkey(),
                config: config_pda(),
                message: message_pda(emitter_chain, sequence),
                guardian_set,
                guardian_signatures: signatures.pubkey(),
                verify_vaa_shim: WORMHOLE_VERIFY_VAA_SHIM,
                system_program: SYSTEM,
                event_authority: event_authority(&PROGRAM),
                program: PROGRAM,
            }
            .to_account_metas(None),
            data: instruction::ReceiveMessage {
                emitter_chain,
                sequence,
                guardian_set_bump: bump,
                body: body.to_vec(),
            }
            .data(),
        };
        self.as_admin(&[receive])
    }

    /// A dispatcher message from Arbitrum, signed by the guardian, with the next sequence.
    fn deliver(&mut self, action: &Instruction) -> u64 {
        let sequence = self.next_sequence;
        self.next_sequence += 1;
        let body = vaa_body(
            ARBITRUM_WORMHOLE_CHAIN,
            dispatcher(),
            sequence,
            &payload(1, LOCALNET_CHAIN_ID, action),
        );
        self.relay(&body, GUARDIAN_KEY)
            .expect("receive a genuine message");
        sequence
    }

    fn execute(
        &mut self,
        sequence: u64,
        treasuries: &[(Pubkey, Pubkey)],
        action: &Instruction,
    ) -> Result<Vec<String>, Vec<String>> {
        let ix = self.execute_ix(sequence, treasuries, action);
        self.as_admin(&[ix])
    }

    fn execute_ix(
        &self,
        sequence: u64,
        treasuries: &[(Pubkey, Pubkey)],
        action: &Instruction,
    ) -> Instruction {
        let mut metas = accounts::Execute {
            executor: self.admin.pubkey(),
            config: config_pda(),
            message: message_pda(ARBITRUM_WORMHOLE_CHAIN, sequence),
            allowed: pda(&[ALLOWED_SEED, action.program_id.as_ref()], &PROGRAM),
            target_program: action.program_id,
            authority: authority(),
            event_authority: event_authority(&PROGRAM),
            program: PROGRAM,
        }
        .to_account_metas(None);
        let mut sorted = treasuries.to_vec();
        sorted.sort();
        for (mint, watched) in sorted {
            metas.push(AccountMeta::new_readonly(
                pda(&[TREASURY_SEED, mint.as_ref()], &PROGRAM),
                false,
            ));
            metas.push(AccountMeta::new(
                pda(&[WINDOW_SEED, mint.as_ref()], &PROGRAM),
                false,
            ));
            metas.push(AccountMeta::new_readonly(watched, false));
        }
        // The authority signs by the receiver's invoke_signed, not in the transaction.
        metas.extend(action.accounts.iter().map(|m| AccountMeta {
            is_signer: false,
            ..m.clone()
        }));
        Instruction {
            program_id: PROGRAM,
            accounts: metas,
            data: instruction::Execute {}.data(),
        }
    }

    fn message(&self, sequence: u64) -> InboundMessage {
        account(&self.svm, &message_pda(ARBITRUM_WORMHOLE_CHAIN, sequence))
    }

    // --- tokens ---

    fn mint(&mut self) -> Pubkey {
        let mint = Keypair::new().pubkey();
        let mut data = vec![0u8; spl_token::state::Mint::LEN];
        spl_token::state::Mint {
            mint_authority: Some(self.admin.pubkey()).into(),
            supply: 0,
            decimals: 6,
            is_initialized: true,
            freeze_authority: None.into(),
        }
        .pack_into_slice(&mut data);
        self.set(mint, data, spl_token::ID);
        mint
    }

    fn token_account(&mut self, mint: Pubkey, owner: Pubkey, amount: u64) -> Pubkey {
        let address = Keypair::new().pubkey();
        let mut data = vec![0u8; spl_token::state::Account::LEN];
        spl_token::state::Account {
            mint,
            owner,
            amount,
            delegate: None.into(),
            state: spl_token::state::AccountState::Initialized,
            is_native: None.into(),
            delegated_amount: 0,
            close_authority: None.into(),
        }
        .pack_into_slice(&mut data);
        self.set(address, data, spl_token::ID);
        address
    }

    fn set(&mut self, address: Pubkey, data: Vec<u8>, owner: Pubkey) {
        let lamports = self.svm.minimum_balance_for_rent_exemption(data.len());
        self.svm
            .set_account(
                address,
                Account {
                    lamports,
                    data,
                    owner,
                    executable: false,
                    rent_epoch: 0,
                },
            )
            .unwrap();
    }

    fn balance(&self, address: &Pubkey) -> u64 {
        spl_token::state::Account::unpack(&self.svm.get_account(address).unwrap().data)
            .unwrap()
            .amount
    }
}

/// A §16.3 message whose payload is `accounts_hash ‖ data` for `action`, with the authority signing.
fn payload(version: u8, target_chain: u64, action: &Instruction) -> Vec<u8> {
    let mut out = vec![version];
    out.extend_from_slice(&42_161u64.to_be_bytes());
    out.extend_from_slice(&7u64.to_be_bytes());
    out.extend_from_slice(&target_chain.to_be_bytes());
    out.extend_from_slice(action.program_id.as_ref());
    out.extend_from_slice(&[0u8; 32]);
    out.extend_from_slice(&voted_hash(&action.accounts));
    out.extend_from_slice(&action.data);
    out
}

// A transaction grants a key one set of privileges, so a key listed twice carries the union of its flags
// at every position. The voted list is written that way.
fn voted_hash(metas: &[AccountMeta]) -> [u8; 32] {
    let merged: Vec<AccountMeta> = metas
        .iter()
        .map(|m| AccountMeta {
            pubkey: m.pubkey,
            is_signer: metas.iter().any(|o| o.pubkey == m.pubkey && o.is_signer),
            is_writable: metas.iter().any(|o| o.pubkey == m.pubkey && o.is_writable),
        })
        .collect();
    accounts_hash(
        merged
            .iter()
            .map(|m| (&m.pubkey, m.is_signer, m.is_writable)),
    )
}

// --- actions a message can carry ---

fn manage_vault(mint: &Pubkey, signer: Pubkey, data: Vec<u8>) -> Instruction {
    Instruction {
        program_id: VAULT,
        accounts: aegis_vault::accounts::Manage {
            signer,
            vault: pda(&[aegis_vault::state::VAULT_SEED, mint.as_ref()], &VAULT),
            event_authority: event_authority(&VAULT),
            program: VAULT,
        }
        .to_account_metas(None),
        data,
    }
}

fn token_transfer(from: Pubkey, to: Pubkey, amount: u64) -> Instruction {
    let mut data = vec![3u8];
    data.extend_from_slice(&amount.to_le_bytes());
    Instruction {
        program_id: spl_token::ID,
        accounts: vec![
            AccountMeta::new(from, false),
            AccountMeta::new(to, false),
            AccountMeta::new_readonly(authority(), true),
        ],
        data,
    }
}

fn token_account_action(tag: u8, account: Pubkey, other: Pubkey, extra: &[u8]) -> Instruction {
    let mut data = vec![tag];
    data.extend_from_slice(extra);
    let mut accounts = vec![AccountMeta::new(account, false)];
    if tag == 4 {
        accounts.push(AccountMeta::new_readonly(other, false));
    }
    accounts.push(AccountMeta::new_readonly(authority(), true));
    Instruction {
        program_id: spl_token::ID,
        accounts,
        data,
    }
}

fn lamport_transfer(to: Pubkey, lamports: u64) -> Instruction {
    let mut data = 2u32.to_le_bytes().to_vec();
    data.extend_from_slice(&lamports.to_le_bytes());
    Instruction {
        program_id: SYSTEM,
        accounts: vec![
            AccountMeta::new(authority(), true),
            AccountMeta::new(to, false),
        ],
        data,
    }
}

fn receiver_call(data: Vec<u8>) -> Instruction {
    Env::govern_ix(&authority(), data)
}

fn with_signer(mut ix: Instruction) -> Instruction {
    for m in ix.accounts.iter_mut() {
        if m.pubkey == authority() {
            m.is_signer = true;
        }
    }
    ix
}

fn vault_with_pending_admin(env: &mut Env) -> Pubkey {
    let mint = env.mint();
    let admin = env.admin.pubkey();
    let init = Instruction {
        program_id: VAULT,
        accounts: aegis_vault::accounts::InitializeVault {
            admin,
            vault: pda(&[aegis_vault::state::VAULT_SEED, mint.as_ref()], &VAULT),
            vault_tokens: pda(
                &[
                    aegis_vault::state::TOKENS_SEED,
                    pda(&[aegis_vault::state::VAULT_SEED, mint.as_ref()], &VAULT).as_ref(),
                ],
                &VAULT,
            ),
            mint,
            this_program: VAULT,
            program_data: program_data(&VAULT),
            token_program: spl_token::ID,
            system_program: SYSTEM,
            event_authority: event_authority(&VAULT),
            program: VAULT,
        }
        .to_account_metas(None),
        data: aegis_vault::instruction::InitializeVault {
            manager: admin,
            pauser: admin,
            deposit_cap: 1_000_000,
            min_deposit: 1,
        }
        .data(),
    };
    let propose = manage_vault(
        &mint,
        admin,
        aegis_vault::instruction::ProposeAdmin {
            proposed: authority(),
        }
        .data(),
    );
    env.as_admin(&[init, propose])
        .expect("vault with the authority as pending admin");
    mint
}

fn vault_admin(env: &Env, mint: &Pubkey) -> Pubkey {
    account::<aegis_vault::state::Vault>(
        &env.svm,
        &pda(&[aegis_vault::state::VAULT_SEED, mint.as_ref()], &VAULT),
    )
    .admin
}

// --- tests ---

#[test]
fn a_governance_message_takes_the_vault_admin_role_after_its_delay() {
    let mut env = Env::new();
    let mint = vault_with_pending_admin(&mut env);
    let action = manage_vault(
        &mint,
        authority(),
        aegis_vault::instruction::AcceptAdmin {}.data(),
    );

    let sequence = env.deliver(&action);
    let stored = env.message(sequence);
    assert_eq!(stored.state, MESSAGE_PENDING);
    assert_eq!(
        (stored.source_chain_id, stored.operation_id, stored.target),
        (42_161, 7, VAULT)
    );
    assert_eq!(stored.executable_at, stored.received_at + DELAY);

    warp(&mut env.svm, DELAY - 1);
    expect_refused(env.execute(sequence, &[], &action), "DelayNotElapsed");

    warp(&mut env.svm, 1);
    env.execute(sequence, &[], &action)
        .expect("execute after the delay");
    assert_eq!(vault_admin(&env, &mint), authority());
    assert_eq!(env.message(sequence).state, MESSAGE_EXECUTED);

    expect_refused(env.execute(sequence, &[], &action), "MessageNotPending");
}

#[test]
fn only_the_dispatcher_on_arbitrum_is_heard() {
    let mut env = Env::new();
    let action = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let good = payload(1, LOCALNET_CHAIN_ID, &action);

    let mut other_address = dispatcher();
    other_address[31] ^= 1;
    let body = vaa_body(ARBITRUM_WORMHOLE_CHAIN, other_address, 1, &good);
    expect_refused(env.relay(&body, GUARDIAN_KEY), "UnknownEmitter");

    let body = vaa_body(2, dispatcher(), 1, &good);
    expect_refused(env.relay(&body, GUARDIAN_KEY), "UnknownEmitter");
}

#[test]
fn a_vaa_the_guardian_set_did_not_sign_is_refused() {
    let mut env = Env::new();
    let action = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let body = vaa_body(
        ARBITRUM_WORMHOLE_CHAIN,
        dispatcher(),
        1,
        &payload(1, LOCALNET_CHAIN_ID, &action),
    );

    let impostor = "1111111111111111111111111111111111111111111111111111111111111111";
    assert!(
        env.relay(&body, impostor).is_err(),
        "signed by a key outside the guardian set"
    );

    // Signed genuinely, then the delay-setting action altered before delivery.
    let signatures = Keypair::new();
    let mut data = discriminator("post_signatures");
    data.extend_from_slice(&0u32.to_le_bytes());
    data.push(1);
    data.extend_from_slice(&1u32.to_le_bytes());
    data.extend_from_slice(&guardian_signature(&body, GUARDIAN_KEY));
    let post = Instruction {
        program_id: WORMHOLE_VERIFY_VAA_SHIM,
        accounts: vec![
            AccountMeta::new(env.admin.pubkey(), true),
            AccountMeta::new(signatures.pubkey(), true),
            AccountMeta::new_readonly(SYSTEM, false),
        ],
        data,
    };
    let admin = env.admin.insecure_clone();
    send(&mut env.svm, &[post], &admin, &[&signatures]).unwrap();
    let mut tampered = body.clone();
    *tampered.last_mut().unwrap() ^= 1;
    let (guardian_set, bump) = guardian_set();
    let receive = Instruction {
        program_id: PROGRAM,
        accounts: accounts::ReceiveMessage {
            payer: env.admin.pubkey(),
            config: config_pda(),
            message: message_pda(ARBITRUM_WORMHOLE_CHAIN, 1),
            guardian_set,
            guardian_signatures: signatures.pubkey(),
            verify_vaa_shim: WORMHOLE_VERIFY_VAA_SHIM,
            system_program: SYSTEM,
            event_authority: event_authority(&PROGRAM),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::ReceiveMessage {
            emitter_chain: ARBITRUM_WORMHOLE_CHAIN,
            sequence: 1,
            guardian_set_bump: bump,
            body: tampered,
        }
        .data(),
    };
    assert!(
        env.as_admin(&[receive]).is_err(),
        "payload altered after signing"
    );
    assert!(env
        .svm
        .get_account(&message_pda(ARBITRUM_WORMHOLE_CHAIN, 1))
        .is_none_or(|a| a.data.is_empty()));

    env.relay(&body, GUARDIAN_KEY)
        .expect("the untouched VAA is still accepted");
}

#[test]
fn a_replayed_vaa_is_refused() {
    let mut env = Env::new();
    let action = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let body = vaa_body(
        ARBITRUM_WORMHOLE_CHAIN,
        dispatcher(),
        1,
        &payload(1, LOCALNET_CHAIN_ID, &action),
    );
    env.relay(&body, GUARDIAN_KEY).expect("first delivery");
    expect_refused(env.relay(&body, GUARDIAN_KEY), "already in use");
}

#[test]
fn a_message_for_another_version_or_cluster_is_refused() {
    let mut env = Env::new();
    let action = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let body = vaa_body(
        ARBITRUM_WORMHOLE_CHAIN,
        dispatcher(),
        1,
        &payload(2, LOCALNET_CHAIN_ID, &action),
    );
    expect_refused(env.relay(&body, GUARDIAN_KEY), "UnsupportedVersion");
    let body = vaa_body(
        ARBITRUM_WORMHOLE_CHAIN,
        dispatcher(),
        2,
        &payload(1, (1 << 62) + 2, &action),
    );
    expect_refused(env.relay(&body, GUARDIAN_KEY), "WrongTargetChain");
}

#[test]
fn a_pause_stops_everything_but_governances_unpause() {
    let mut env = Env::new();
    env.finish_bootstrap();
    let set_delay = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let waiting = env.deliver(&set_delay);
    warp(&mut env.svm, DELAY);

    let stranger = Keypair::new();
    env.svm.airdrop(&stranger.pubkey(), 1_000_000_000).unwrap();
    expect_refused(
        env.as_signer(
            &stranger,
            &[Env::govern_ix(
                &stranger.pubkey(),
                instruction::Pause {}.data(),
            )],
        ),
        "Unauthorized",
    );
    let guardian = env.guardian.insecure_clone();
    env.as_signer(
        &guardian,
        &[Env::govern_ix(
            &guardian.pubkey(),
            instruction::Pause {}.data(),
        )],
    )
    .expect("guardian pauses");

    expect_refused(env.execute(waiting, &[], &set_delay), "Paused");
    let body = vaa_body(
        ARBITRUM_WORMHOLE_CHAIN,
        dispatcher(),
        99,
        &payload(1, LOCALNET_CHAIN_ID, &set_delay),
    );
    expect_refused(env.relay(&body, GUARDIAN_KEY), "Paused");

    expect_refused(
        env.as_signer(
            &guardian,
            &[Env::govern_ix(
                &guardian.pubkey(),
                instruction::Unpause {}.data(),
            )],
        ),
        "Unauthorized",
    );
    let admin = env.admin.pubkey();
    expect_refused(
        env.as_admin(&[Env::govern_ix(&admin, instruction::Unpause {}.data())]),
        "Unauthorized",
    );

    // The same bytes addressed to another program are not an unpause.
    let lookalike = Instruction {
        program_id: VAULT,
        ..receiver_call(instruction::Unpause {}.data())
    };
    let body = vaa_body(
        ARBITRUM_WORMHOLE_CHAIN,
        dispatcher(),
        98,
        &payload(1, LOCALNET_CHAIN_ID, &lookalike),
    );
    expect_refused(env.relay(&body, GUARDIAN_KEY), "Paused");

    let unpause = receiver_call(instruction::Unpause {}.data());
    let lift = env.deliver(&unpause);
    expect_refused(env.execute(lift, &[], &unpause), "DelayNotElapsed");
    warp(&mut env.svm, DELAY);
    env.execute(lift, &[], &unpause)
        .expect("governance unpauses");
    assert!(!env.config().paused);
    env.execute(waiting, &[], &set_delay)
        .expect("the waiting message runs once unpaused");
    assert_eq!(env.config().delay, 7_200);
}

#[test]
fn a_cancelled_message_never_executes() {
    let mut env = Env::new();
    let action = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let sequence = env.deliver(&action);
    let cancel = |signer: Pubkey| Instruction {
        program_id: PROGRAM,
        accounts: accounts::Cancel {
            signer,
            config: config_pda(),
            message: message_pda(ARBITRUM_WORMHOLE_CHAIN, sequence),
            event_authority: event_authority(&PROGRAM),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::Cancel {}.data(),
    };
    env.finish_bootstrap();
    expect_refused(env.as_admin(&[cancel(env.admin.pubkey())]), "Unauthorized");
    let guardian = env.guardian.insecure_clone();
    env.as_signer(&guardian, &[cancel(guardian.pubkey())])
        .expect("guardian cancels");
    assert_eq!(env.message(sequence).state, MESSAGE_CANCELLED);

    warp(&mut env.svm, DELAY);
    expect_refused(env.execute(sequence, &[], &action), "MessageNotPending");
    expect_refused(
        env.as_signer(&guardian, &[cancel(guardian.pubkey())]),
        "MessageNotPending",
    );
    assert_eq!(env.config().delay, DELAY);
}

#[test]
fn the_executor_cannot_change_the_voted_accounts() {
    let mut env = Env::new();
    let mint = vault_with_pending_admin(&mut env);
    let action = manage_vault(
        &mint,
        authority(),
        aegis_vault::instruction::AcceptAdmin {}.data(),
    );
    let sequence = env.deliver(&action);
    warp(&mut env.svm, DELAY);

    let mut extra = action.clone();
    extra
        .accounts
        .push(AccountMeta::new_readonly(Keypair::new().pubkey(), false));
    expect_refused(env.execute(sequence, &[], &extra), "AccountsMismatch");

    let other_vault = vault_with_pending_admin(&mut env);
    let swapped = manage_vault(&other_vault, authority(), action.data.clone());
    expect_refused(env.execute(sequence, &[], &swapped), "AccountsMismatch");

    let mut writable = action.clone();
    writable.accounts[2].is_writable = true;
    expect_refused(env.execute(sequence, &[], &writable), "AccountsMismatch");

    // The authority is always the signer, so a list voted with it as a non-signer never runs.
    let mut unsigned = action.clone();
    unsigned.accounts[0].is_signer = false;
    let unsigned_sequence = env.deliver(&unsigned);
    warp(&mut env.svm, DELAY);
    expect_refused(
        env.execute(unsigned_sequence, &[], &unsigned),
        "AccountsMismatch",
    );

    env.execute(sequence, &[], &action).expect("the voted list");
    assert_eq!(vault_admin(&env, &mint), authority());
}

#[test]
fn only_allowlisted_programs_are_called() {
    let mut env = Env::new();
    let mint = vault_with_pending_admin(&mut env);
    let action = manage_vault(
        &mint,
        authority(),
        aegis_vault::instruction::AcceptAdmin {}.data(),
    );
    let sequence = env.deliver(&action);
    warp(&mut env.svm, DELAY);

    let admin = env.admin.pubkey();
    let ix = env.set_allowed_ix(&admin, VAULT, false);
    env.as_admin(&[ix]).unwrap();
    expect_refused(env.execute(sequence, &[], &action), "ProgramNotAllowed");

    let stranger_program = Keypair::new().pubkey();
    let mut foreign = action.clone();
    foreign.program_id = stranger_program;
    let foreign_sequence = env.deliver(&foreign);
    warp(&mut env.svm, DELAY);
    expect_refused(
        env.execute(foreign_sequence, &[], &foreign),
        "AccountNotInitialized",
    );

    let ix = env.set_allowed_ix(&admin, VAULT, true);
    env.as_admin(&[ix]).unwrap();
    env.execute(sequence, &[], &action).expect("allowed again");
}

fn token_treasury(
    env: &mut Env,
    amount: u64,
    per_message_cap: u64,
    rolling_cap: u64,
) -> (Pubkey, Pubkey, Pubkey) {
    let mint = env.mint();
    let treasury = env.token_account(mint, authority(), amount);
    let recipient = env.token_account(mint, Keypair::new().pubkey(), 0);
    env.register_treasury(mint, treasury, per_message_cap, rolling_cap)
        .expect("register the treasury");
    (mint, treasury, recipient)
}

#[test]
fn token_outflow_is_capped_per_message_and_over_a_rolling_window() {
    let mut env = Env::new();
    let (mint, treasury, recipient) = token_treasury(&mut env, 1_000, 100, 150);
    let treasuries = [(mint, treasury)];

    let over = token_transfer(treasury, recipient, 101);
    let first = token_transfer(treasury, recipient, 100);
    let second = token_transfer(treasury, recipient, 51);
    let fits = token_transfer(treasury, recipient, 50);
    let (s_over, s_first, s_second, s_fits) = (
        env.deliver(&over),
        env.deliver(&first),
        env.deliver(&second),
        env.deliver(&fits),
    );
    warp(&mut env.svm, DELAY);

    expect_refused(
        env.execute(s_over, &treasuries, &over),
        "PerMessageCapExceeded",
    );
    assert_eq!(
        env.balance(&treasury),
        1_000,
        "the refused transfer is reverted"
    );

    env.execute(s_first, &treasuries, &first)
        .expect("at the per-message cap");
    expect_refused(
        env.execute(s_second, &treasuries, &second),
        "RollingCapExceeded",
    );
    env.execute(s_fits, &treasuries, &fits)
        .expect("exactly the rolling cap");
    assert_eq!(env.balance(&recipient), 150);

    let later = token_transfer(treasury, recipient, 1);
    let s_later = env.deliver(&later);
    warp(&mut env.svm, DELAY);
    expect_refused(
        env.execute(s_later, &treasuries, &later),
        "RollingCapExceeded",
    );
    warp(&mut env.svm, WINDOW - DELAY - 1);
    expect_refused(
        env.execute(s_second, &treasuries, &second),
        "RollingCapExceeded",
    );
    warp(&mut env.svm, 1);
    env.execute(s_second, &treasuries, &second)
        .expect("a full window after the first outflows");
}

#[test]
fn lamports_leaving_the_authority_are_capped() {
    let mut env = Env::new();
    env.svm.airdrop(&authority(), 10_000_000_000).unwrap();
    env.register_treasury(LAMPORTS_MINT, authority(), 1_000_000, 1_000_000)
        .unwrap();
    let treasuries = [(LAMPORTS_MINT, authority())];
    let recipient = Keypair::new().pubkey();

    let over = lamport_transfer(recipient, 1_000_001);
    let fits = lamport_transfer(recipient, 1_000_000);
    let (s_over, s_fits) = (env.deliver(&over), env.deliver(&fits));
    warp(&mut env.svm, DELAY);
    expect_refused(
        env.execute(s_over, &treasuries, &over),
        "PerMessageCapExceeded",
    );
    env.execute(s_fits, &treasuries, &fits)
        .expect("within the cap");
    assert_eq!(env.svm.get_balance(&recipient), Some(1_000_000));
}

#[test]
fn giving_away_control_of_a_treasury_account_is_an_outflow() {
    let mut env = Env::new();
    let (mint, treasury, _) = token_treasury(&mut env, 1_000, 100, 1_000);
    let treasuries = [(mint, treasury)];

    let mut new_owner = vec![2u8, 1];
    new_owner.extend_from_slice(Keypair::new().pubkey().as_ref());
    let hand_over = token_account_action(6, treasury, Pubkey::default(), &new_owner);
    let approve = token_account_action(4, treasury, Keypair::new().pubkey(), &1u64.to_le_bytes());
    let (s_hand_over, s_approve) = (env.deliver(&hand_over), env.deliver(&approve));
    warp(&mut env.svm, DELAY);

    expect_refused(
        env.execute(s_hand_over, &treasuries, &hand_over),
        "PerMessageCapExceeded",
    );
    expect_refused(
        env.execute(s_approve, &treasuries, &approve),
        "TreasuryDelegated",
    );
}

#[test]
fn every_registered_treasury_must_be_presented_as_registered() {
    let mut env = Env::new();
    let (mint, treasury, recipient) = token_treasury(&mut env, 1_000, 100, 1_000);
    let (other_mint, other_treasury, _) = token_treasury(&mut env, 1_000, 100, 1_000);
    let action = token_transfer(treasury, recipient, 500);
    let sequence = env.deliver(&action);
    warp(&mut env.svm, DELAY);

    expect_refused(
        env.execute(sequence, &[], &action),
        "TreasuryListIncomplete",
    );
    expect_refused(
        env.execute(sequence, &[(mint, treasury)], &action),
        "TreasuryMismatch",
    );
    expect_refused(
        env.execute(
            sequence,
            &[(mint, recipient), (other_mint, other_treasury)],
            &action,
        ),
        "TreasuryMismatch",
    );

    let mut duplicated = env.execute_ix(
        sequence,
        &[(mint, treasury), (other_mint, other_treasury)],
        &action,
    );
    let first_triple: Vec<AccountMeta> = duplicated.accounts[8..11].to_vec();
    duplicated.accounts[11..14].clone_from_slice(&first_triple);
    expect_refused(env.as_admin(&[duplicated]), "TreasuryMismatch");

    let mut read_only_window = env.execute_ix(
        sequence,
        &[(mint, treasury), (other_mint, other_treasury)],
        &action,
    );
    read_only_window.accounts[9].is_writable = false;
    expect_refused(env.as_admin(&[read_only_window]), "TreasuryMismatch");

    expect_refused(
        env.execute(
            sequence,
            &[(mint, treasury), (other_mint, other_treasury)],
            &action,
        ),
        "PerMessageCapExceeded",
    );
}

#[test]
fn a_treasury_must_be_the_authority_or_a_token_account_it_owns() {
    let mut env = Env::new();
    let mint = env.mint();
    let foreign = env.token_account(mint, Keypair::new().pubkey(), 10);
    expect_refused(
        env.register_treasury(mint, foreign, 1, 1),
        "InvalidTreasuryAccount",
    );
    expect_refused(
        env.register_treasury(LAMPORTS_MINT, env.admin.pubkey(), 1, 1),
        "InvalidTreasuryAccount",
    );
    let other_mint = env.mint();
    let wrong_mint = env.token_account(other_mint, authority(), 10);
    expect_refused(
        env.register_treasury(mint, wrong_mint, 1, 1),
        "InvalidTreasuryAccount",
    );
    let held = env.token_account(mint, authority(), 10);
    expect_refused(env.register_treasury(mint, held, 2, 1), "InvalidParameter");
    env.register_treasury(mint, held, 1, 2)
        .expect("a token account the authority owns");
    assert_eq!(env.config().treasury_count, 1);
}

#[test]
fn parameters_change_only_through_governance_once_bootstrap_ends() {
    let mut env = Env::new();
    let admin = env.admin.pubkey();
    expect_refused(
        env.as_admin(&[Env::govern_ix(
            &admin,
            instruction::SetDelay { delay: DELAY - 1 }.data(),
        )]),
        "DelayTooShort",
    );
    env.as_admin(&[Env::govern_ix(
        &admin,
        instruction::SetWindow { window: WINDOW * 2 }.data(),
    )])
    .expect("bootstrap admin during bootstrap");
    env.finish_bootstrap();
    expect_refused(
        env.as_admin(&[Env::govern_ix(
            &admin,
            instruction::FinishBootstrap {}.data(),
        )]),
        "BootstrapFinished",
    );

    let guardian = env.guardian.insecure_clone();
    let calls: Vec<(&str, Vec<u8>)> = vec![
        ("set_delay", instruction::SetDelay { delay: 7_200 }.data()),
        ("set_window", instruction::SetWindow { window: 1 }.data()),
        (
            "set_emitter",
            instruction::SetEmitter {
                emitter_chain: 2,
                emitter_address: [9; 32],
            }
            .data(),
        ),
        (
            "set_guardian",
            instruction::SetGuardian { guardian: admin }.data(),
        ),
        ("unpause", instruction::Unpause {}.data()),
    ];
    for (name, data) in &calls {
        for signer in [&env.admin.insecure_clone(), &guardian] {
            let result = env.as_signer(signer, &[Env::govern_ix(&signer.pubkey(), data.clone())]);
            assert!(
                result.is_err_and(|logs| logs.iter().any(|l| l.contains("Unauthorized"))),
                "{name} by a key"
            );
        }
    }
    let allow = env.set_allowed_ix(&admin, Keypair::new().pubkey(), true);
    expect_refused(env.as_admin(&[allow]), "Unauthorized");
    let mint = env.mint();
    let held = env.token_account(mint, authority(), 10);
    expect_refused(env.register_treasury(mint, held, 1, 1), "Unauthorized");

    let set_delay = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let sequence = env.deliver(&set_delay);
    warp(&mut env.svm, DELAY);
    env.execute(sequence, &[], &set_delay)
        .expect("governance sets the delay");
    assert_eq!(env.config().delay, 7_200);

    let too_short = receiver_call(instruction::SetDelay { delay: DELAY - 1 }.data());
    let sequence = env.deliver(&too_short);
    warp(&mut env.svm, 7_200);
    expect_refused(env.execute(sequence, &[], &too_short), "DelayTooShort");
}

#[test]
fn a_message_cannot_execute_another_from_inside_its_call() {
    let mut env = Env::new();
    let set_delay = receiver_call(instruction::SetDelay { delay: 7_200 }.data());
    let inner = env.deliver(&set_delay);
    let mut nested = env.execute_ix(inner, &[], &set_delay);
    nested.accounts[0] = AccountMeta::new_readonly(authority(), true);
    let nested = with_signer(nested);
    let outer = env.deliver(&nested);
    warp(&mut env.svm, DELAY);

    expect_refused(env.execute(outer, &[], &nested), "NotTopLevel");
    assert_eq!(env.message(inner).state, MESSAGE_PENDING);
}

#[test]
fn only_the_upgrade_authority_initializes() {
    let mut svm = LiteSVM::new();
    let root = concat!(env!("CARGO_MANIFEST_DIR"), "/../..");
    load(
        &mut svm,
        PROGRAM,
        &format!("{root}/target/deploy/aegis_governance_receiver.so"),
    );
    let (admin, outsider) = (Keypair::new(), Keypair::new());
    svm.airdrop(&admin.pubkey(), 10_000_000_000).unwrap();
    svm.airdrop(&outsider.pubkey(), 10_000_000_000).unwrap();
    set_upgrade_authority(&mut svm, &PROGRAM, &admin.pubkey());
    let mut env = Env {
        svm,
        admin,
        guardian: Keypair::new(),
        next_sequence: 1,
    };

    let ix = env.initialize_ix(&outsider.pubkey());
    expect_refused(env.as_signer(&outsider, &[ix]), "NotUpgradeAuthority");
    let ix = env.initialize_ix(&env.admin.pubkey());
    env.as_admin(&[ix])
        .expect("the upgrade authority initializes");
    let config = env.config();
    assert!(config.bootstrapping);
    assert_eq!(
        (
            config.bootstrap_admin,
            config.chain_id,
            config.emitter_address
        ),
        (env.admin.pubkey(), LOCALNET_CHAIN_ID, dispatcher())
    );
}

// The largest instruction a message may carry still fits in one receive transaction.
#[test]
fn a_message_at_the_instruction_data_limit_fits_one_transaction() {
    let mut env = Env::new();
    let mut action = receiver_call(vec![
        7;
        aegis_governance_receiver::message::MAX_INSTRUCTION_DATA
    ]);
    let sequence = env.deliver(&action);
    assert_eq!(
        env.message(sequence).data_len as usize,
        aegis_governance_receiver::message::MAX_INSTRUCTION_DATA
    );

    action.data.push(7);
    let body = vaa_body(
        ARBITRUM_WORMHOLE_CHAIN,
        dispatcher(),
        2,
        &payload(1, LOCALNET_CHAIN_ID, &action),
    );
    expect_refused(env.relay(&body, GUARDIAN_KEY), "InstructionDataTooLong");
}

#[test]
fn a_deregistered_treasury_is_no_longer_presented() {
    let mut env = Env::new();
    let (mint, treasury, recipient) = token_treasury(&mut env, 1_000, 100, 1_000);
    let (other_mint, other_treasury, _) = token_treasury(&mut env, 1_000, 100, 1_000);
    let deregister = Instruction {
        program_id: PROGRAM,
        accounts: accounts::DeregisterTreasury {
            controller: env.admin.pubkey(),
            config: config_pda(),
            treasury: pda(&[TREASURY_SEED, other_mint.as_ref()], &PROGRAM),
            window: pda(&[WINDOW_SEED, other_mint.as_ref()], &PROGRAM),
            event_authority: event_authority(&PROGRAM),
            program: PROGRAM,
        }
        .to_account_metas(None),
        data: instruction::DeregisterTreasury {}.data(),
    };
    env.as_admin(&[deregister]).expect("deregister");
    assert_eq!(env.config().treasury_count, 1);

    let action = token_transfer(treasury, recipient, 100);
    let sequence = env.deliver(&action);
    warp(&mut env.svm, DELAY);
    // The deregistered treasury's accounts, placed after the one still registered, are read as the
    // instruction's accounts, which were not voted.
    let mut stale = env.execute_ix(sequence, &[(mint, treasury)], &action);
    let extra = [
        AccountMeta::new_readonly(pda(&[TREASURY_SEED, other_mint.as_ref()], &PROGRAM), false),
        AccountMeta::new(pda(&[WINDOW_SEED, other_mint.as_ref()], &PROGRAM), false),
        AccountMeta::new_readonly(other_treasury, false),
    ];
    stale.accounts.splice(11..11, extra);
    expect_refused(env.as_admin(&[stale]), "AccountsMismatch");
    env.execute(sequence, &[(mint, treasury)], &action)
        .expect("the remaining treasury alone");
}
