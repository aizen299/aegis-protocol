//! zk proof service (v0.4).
//!
//! The binary is a thin shell around this library so the integration tests in zk/tests can drive
//! the same code the server does, rather than a copy of it.
//!
//! Private inputs are never logged and are zeroized after use.

pub mod api;
pub mod prover;
pub mod types;
