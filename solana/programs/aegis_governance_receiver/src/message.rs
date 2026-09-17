use anchor_lang::prelude::*;

use crate::error::ReceiverError;

pub const MESSAGE_VERSION: u8 = 1;
pub const MAX_INSTRUCTION_DATA: usize = 512;
const VAA_HEADER_LEN: usize = 4 + 4 + 2 + 32 + 8 + 1;
const MESSAGE_HEADER_LEN: usize = 1 + 8 + 8 + 8 + 32 + 32;
const ACCOUNTS_HASH_LEN: usize = 32;

/// The parts of a VAA body the receiver reads: who sent it, which of their messages it is, and the
/// payload.
pub struct VaaBody<'a> {
    pub emitter_chain: u16,
    pub emitter_address: [u8; 32],
    pub sequence: u64,
    pub payload: &'a [u8],
}

/// A version-1 governance message. docs/v2.0-solana-plan.md §16.3 and §18.3.
#[derive(Debug, PartialEq, Eq)]
pub struct GovernanceMessage<'a> {
    pub source_chain_id: u64,
    pub operation_id: u64,
    pub target_chain_id: u64,
    pub target: Pubkey,
    pub declared_value: [u8; 32],
    pub accounts_hash: [u8; 32],
    pub instruction_data: &'a [u8],
}

fn be_u64(bytes: &[u8]) -> u64 {
    u64::from_be_bytes(bytes.try_into().unwrap())
}

pub fn parse_vaa_body(body: &[u8]) -> Result<VaaBody<'_>> {
    require!(body.len() >= VAA_HEADER_LEN, ReceiverError::MalformedVaa);
    Ok(VaaBody {
        emitter_chain: u16::from_be_bytes([body[8], body[9]]),
        emitter_address: body[10..42].try_into().unwrap(),
        sequence: be_u64(&body[42..50]),
        payload: &body[VAA_HEADER_LEN..],
    })
}

pub fn parse_message(payload: &[u8]) -> Result<GovernanceMessage<'_>> {
    require!(
        payload.len() >= MESSAGE_HEADER_LEN + ACCOUNTS_HASH_LEN,
        ReceiverError::MalformedMessage
    );
    require!(
        payload[0] == MESSAGE_VERSION,
        ReceiverError::UnsupportedVersion
    );
    let data = &payload[MESSAGE_HEADER_LEN + ACCOUNTS_HASH_LEN..];
    require!(
        data.len() <= MAX_INSTRUCTION_DATA,
        ReceiverError::InstructionDataTooLong
    );
    Ok(GovernanceMessage {
        source_chain_id: be_u64(&payload[1..9]),
        operation_id: be_u64(&payload[9..17]),
        target_chain_id: be_u64(&payload[17..25]),
        target: Pubkey::new_from_array(payload[25..57].try_into().unwrap()),
        declared_value: payload[57..89].try_into().unwrap(),
        accounts_hash: payload[89..121].try_into().unwrap(),
        instruction_data: data,
    })
}

/// SHA-256 over each account's key, signer flag, and writable flag, in order. The DAO votes on this, so
/// the executor cannot choose which accounts an action touches or which of them the authority signs.
/// A transaction gives a key one set of privileges, so a key listed twice carries the union of its flags
/// at every position, and the authority is always a signer.
pub fn accounts_hash<'a>(accounts: impl IntoIterator<Item = (&'a Pubkey, bool, bool)>) -> [u8; 32] {
    let mut bytes = Vec::new();
    for (key, is_signer, is_writable) in accounts {
        bytes.extend_from_slice(key.as_ref());
        bytes.push(is_signer as u8);
        bytes.push(is_writable as u8);
    }
    solana_sha256_hasher::hash(&bytes).to_bytes()
}

/// The digest Wormhole guardians sign: keccak256 of keccak256 of the body.
pub fn vaa_digest(body: &[u8]) -> [u8; 32] {
    solana_keccak_hasher::hash(solana_keccak_hasher::hash(body).as_ref()).to_bytes()
}

#[cfg(test)]
pub mod tests {
    use super::*;

    pub fn encode_message(
        target_chain: u64,
        target: &Pubkey,
        accounts_hash: &[u8; 32],
        data: &[u8],
    ) -> Vec<u8> {
        let mut out = vec![MESSAGE_VERSION];
        out.extend_from_slice(&42161u64.to_be_bytes());
        out.extend_from_slice(&7u64.to_be_bytes());
        out.extend_from_slice(&target_chain.to_be_bytes());
        out.extend_from_slice(target.as_ref());
        let mut value = [0u8; 32];
        value[31] = 5;
        out.extend_from_slice(&value);
        out.extend_from_slice(accounts_hash);
        out.extend_from_slice(data);
        out
    }

    #[test]
    fn a_message_parses_into_its_fields() {
        let target = Pubkey::new_from_array([9; 32]);
        let payload = encode_message(3, &target, &[4; 32], &[1, 2, 3]);
        let m = parse_message(&payload).unwrap();
        assert_eq!(
            (m.source_chain_id, m.operation_id, m.target_chain_id),
            (42161, 7, 3)
        );
        assert_eq!(m.target, target);
        assert_eq!(m.declared_value[31], 5);
        assert_eq!(m.accounts_hash, [4; 32]);
        assert_eq!(m.instruction_data, &[1, 2, 3]);
    }

    #[test]
    fn a_malformed_or_foreign_message_is_refused() {
        let good = encode_message(3, &Pubkey::default(), &[0; 32], &[]);
        assert!(parse_message(&good[..good.len() - 1]).is_err(), "truncated");
        let mut v2 = good.clone();
        v2[0] = 2;
        assert!(parse_message(&v2).is_err(), "version 2");
        let long = encode_message(
            3,
            &Pubkey::default(),
            &[0; 32],
            &[0; MAX_INSTRUCTION_DATA + 1],
        );
        assert!(
            parse_message(&long).is_err(),
            "instruction data over the limit"
        );
        let max = encode_message(3, &Pubkey::default(), &[0; 32], &[0; MAX_INSTRUCTION_DATA]);
        assert!(parse_message(&max).is_ok(), "instruction data at the limit");
    }

    #[test]
    fn a_vaa_body_parses_its_emitter_and_sequence() {
        let mut body = vec![0u8; 4 + 4];
        body.extend_from_slice(&23u16.to_be_bytes());
        body.extend_from_slice(&[7; 32]);
        body.extend_from_slice(&99u64.to_be_bytes());
        body.push(1);
        body.extend_from_slice(b"payload");
        let v = parse_vaa_body(&body).unwrap();
        assert_eq!(
            (v.emitter_chain, v.emitter_address, v.sequence, v.payload),
            (23, [7; 32], 99, &b"payload"[..])
        );
        assert!(parse_vaa_body(&body[..50]).is_err());
    }

    // Any difference in key, order, signer flag, or writable flag is a different vote.
    #[test]
    fn the_accounts_hash_binds_keys_order_and_flags() {
        let (a, b) = (
            Pubkey::new_from_array([1; 32]),
            Pubkey::new_from_array([2; 32]),
        );
        let base = accounts_hash([(&a, false, true), (&b, true, false)]);
        assert_ne!(
            base,
            accounts_hash([(&b, true, false), (&a, false, true)]),
            "order"
        );
        assert_ne!(
            base,
            accounts_hash([(&a, true, true), (&b, true, false)]),
            "signer"
        );
        assert_ne!(
            base,
            accounts_hash([(&a, false, false), (&b, true, false)]),
            "writable"
        );
        assert_ne!(
            base,
            accounts_hash([(&a, false, true)]),
            "an account dropped"
        );
        assert_eq!(base, accounts_hash([(&a, false, true), (&b, true, false)]));
    }
}
