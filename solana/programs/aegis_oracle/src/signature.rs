use anchor_lang::prelude::*;

use crate::error::OracleError;

pub const SUBMISSION_DOMAIN: &[u8] = b"aegis-oracle-submission-v1";
pub const SUBMISSION_MESSAGE_LEN: usize = 26 + 32 + 8 + 8 + 32 + 16 + 32 + 8;

const PUBKEY_LEN: usize = 32;
const SIGNATURE_LEN: usize = 64;
const OFFSETS_START: usize = 2;
const OFFSETS_LEN: usize = 14;
// The Ed25519 program reads from the verifying instruction's own data at this index.
const OWN_DATA: u16 = u16::MAX;

// The bytes a node signs. Carries the program and chain so an attestation for one deployment or
// cluster can never be replayed against another. docs/v2.0-solana-plan.md §12.3.
pub fn submission_message(
    program: &Pubkey,
    chain_id: i64,
    round_id: u64,
    feed_id: &[u8; 32],
    value: u128,
    node: &Pubkey,
    nonce: u64,
) -> [u8; SUBMISSION_MESSAGE_LEN] {
    let mut out = [0u8; SUBMISSION_MESSAGE_LEN];
    let mut at = 0;
    let mut put = |bytes: &[u8]| {
        out[at..at + bytes.len()].copy_from_slice(bytes);
        at += bytes.len();
    };
    put(SUBMISSION_DOMAIN);
    put(program.as_ref());
    put(&chain_id.to_le_bytes());
    put(&round_id.to_le_bytes());
    put(feed_id);
    put(&value.to_le_bytes());
    put(node.as_ref());
    put(&nonce.to_le_bytes());
    out
}

fn u16_at(data: &[u8], at: usize) -> Result<usize> {
    let bytes = data
        .get(at..at + 2)
        .ok_or(OracleError::InvalidSignatureInstruction)?;
    Ok(u16::from_le_bytes([bytes[0], bytes[1]]) as usize)
}

fn slice(data: &[u8], offset: usize, len: usize) -> Result<&[u8]> {
    data.get(
        offset
            ..offset
                .checked_add(len)
                .ok_or(OracleError::InvalidSignatureInstruction)?,
    )
    .ok_or_else(|| OracleError::InvalidSignatureInstruction.into())
}

// Checks that an instruction verified exactly one ed25519 signature, by `signer`, over `message`,
// with every offset reading from that instruction's own data. The runtime has already verified the
// signature if the instruction exists; what is checked here is that it verified the right thing.
// Returns the signature, which the submission event carries.
pub fn verified_signature(
    program_id: &Pubkey,
    accounts_len: usize,
    data: &[u8],
    signer: &Pubkey,
    message: &[u8],
) -> Result<[u8; SIGNATURE_LEN]> {
    require_keys_eq!(
        *program_id,
        solana_sdk_ids::ed25519_program::ID,
        OracleError::InvalidSignatureInstruction
    );
    require!(accounts_len == 0, OracleError::InvalidSignatureInstruction);
    require!(
        data.len() >= OFFSETS_START + OFFSETS_LEN && data[0] == 1,
        OracleError::InvalidSignatureInstruction
    );

    let base = OFFSETS_START;
    let signature_offset = u16_at(data, base)?;
    let signature_index = u16_at(data, base + 2)?;
    let pubkey_offset = u16_at(data, base + 4)?;
    let pubkey_index = u16_at(data, base + 6)?;
    let message_offset = u16_at(data, base + 8)?;
    let message_size = u16_at(data, base + 10)?;
    let message_index = u16_at(data, base + 12)?;

    let own = OWN_DATA as usize;
    require!(
        signature_index == own && pubkey_index == own && message_index == own,
        OracleError::InvalidSignatureInstruction
    );

    require!(
        slice(data, pubkey_offset, PUBKEY_LEN)? == signer.as_ref(),
        OracleError::InvalidSignature
    );
    require!(
        message_size == message.len() && slice(data, message_offset, message_size)? == message,
        OracleError::InvalidSignature
    );

    let mut signature = [0u8; SIGNATURE_LEN];
    signature.copy_from_slice(slice(data, signature_offset, SIGNATURE_LEN)?);
    Ok(signature)
}

#[cfg(test)]
mod tests {
    use super::*;

    const ED25519: Pubkey = solana_sdk_ids::ed25519_program::ID;

    // The layout Solana's own builder produces: one signature, offsets reading from this data.
    fn instruction_data(pubkey: &Pubkey, signature: &[u8; 64], message: &[u8]) -> Vec<u8> {
        let pubkey_offset = (OFFSETS_START + OFFSETS_LEN) as u16;
        let signature_offset = pubkey_offset + 32;
        let message_offset = signature_offset + 64;
        let mut data = vec![1u8, 0];
        for v in [
            signature_offset,
            OWN_DATA,
            pubkey_offset,
            OWN_DATA,
            message_offset,
            message.len() as u16,
            OWN_DATA,
        ] {
            data.extend_from_slice(&v.to_le_bytes());
        }
        data.extend_from_slice(pubkey.as_ref());
        data.extend_from_slice(signature);
        data.extend_from_slice(message);
        data
    }

    fn fixture() -> (Pubkey, [u8; 64], Vec<u8>) {
        let signer = Pubkey::new_from_array([7; 32]);
        let message = submission_message(&crate::ID, 1, 2, &[3; 32], 4, &signer, 5).to_vec();
        (signer, [9; 64], message)
    }

    #[test]
    fn the_builders_layout_is_accepted_and_returns_the_signature() {
        let (signer, sig, message) = fixture();
        let data = instruction_data(&signer, &sig, &message);
        assert_eq!(
            verified_signature(&ED25519, 0, &data, &signer, &message).unwrap(),
            sig
        );
    }

    #[test]
    fn the_message_binds_every_field() {
        let node = Pubkey::new_from_array([7; 32]);
        let base = submission_message(&crate::ID, 1, 2, &[3; 32], 4, &node, 5);
        let variants = [
            submission_message(
                &Pubkey::new_from_array([1; 32]),
                1,
                2,
                &[3; 32],
                4,
                &node,
                5,
            ),
            submission_message(&crate::ID, 9, 2, &[3; 32], 4, &node, 5),
            submission_message(&crate::ID, 1, 9, &[3; 32], 4, &node, 5),
            submission_message(&crate::ID, 1, 2, &[9; 32], 4, &node, 5),
            submission_message(&crate::ID, 1, 2, &[3; 32], 9, &node, 5),
            submission_message(
                &crate::ID,
                1,
                2,
                &[3; 32],
                4,
                &Pubkey::new_from_array([9; 32]),
                5,
            ),
            submission_message(&crate::ID, 1, 2, &[3; 32], 4, &node, 9),
        ];
        for (i, v) in variants.iter().enumerate() {
            assert_ne!(&base, v, "field {i} does not change the message");
        }
        assert!(base.starts_with(SUBMISSION_DOMAIN));
    }

    #[test]
    fn everything_but_the_exact_verification_is_refused() {
        let (signer, sig, message) = fixture();
        let good = instruction_data(&signer, &sig, &message);
        let check = |program: &Pubkey, accounts: usize, data: &[u8], key: &Pubkey, msg: &[u8]| {
            verified_signature(program, accounts, data, key, msg).is_err()
        };

        assert!(
            check(
                &Pubkey::new_from_array([5; 32]),
                0,
                &good,
                &signer,
                &message
            ),
            "another program"
        );
        assert!(
            check(&ED25519, 1, &good, &signer, &message),
            "accounts attached"
        );
        assert!(
            check(
                &ED25519,
                0,
                &good,
                &Pubkey::new_from_array([8; 32]),
                &message
            ),
            "another signer"
        );

        let mut other = message.clone();
        other[40] ^= 1;
        assert!(
            check(&ED25519, 0, &good, &signer, &other),
            "another message"
        );
        assert!(
            check(&ED25519, 0, &good, &signer, &message[..message.len() - 1]),
            "a prefix of the message"
        );

        let mut two = good.clone();
        two[0] = 2;
        assert!(
            check(&ED25519, 0, &two, &signer, &message),
            "two signatures"
        );

        for field in [2usize, 6, 12] {
            let mut indexed = good.clone();
            indexed[OFFSETS_START + field..OFFSETS_START + field + 2]
                .copy_from_slice(&0u16.to_le_bytes());
            assert!(
                check(&ED25519, 0, &indexed, &signer, &message),
                "index field {field} reads another instruction"
            );
        }

        for field in [0usize, 4, 8] {
            let mut out_of_range = good.clone();
            out_of_range[OFFSETS_START + field..OFFSETS_START + field + 2]
                .copy_from_slice(&u16::MAX.to_le_bytes());
            assert!(
                check(&ED25519, 0, &out_of_range, &signer, &message),
                "offset field {field} past the data"
            );
        }

        assert!(
            check(
                &ED25519,
                0,
                &good[..OFFSETS_START + OFFSETS_LEN - 1],
                &signer,
                &message
            ),
            "truncated header"
        );
        assert!(
            check(&ED25519, 0, &good[..good.len() - 1], &signer, &message),
            "truncated message"
        );
    }
}
