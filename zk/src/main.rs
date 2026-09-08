//! zk proof service (v0.4).
//!
//! Scaffold only. The circuit framework decision (Circom vs Noir) is open — see docs/zk.md.
//! Private inputs must never be logged and are zeroized after use.

mod api;
mod prover;
mod types;

use std::net::SocketAddr;

use tokio::signal;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();

    let addr: SocketAddr = std::env::var("ZK_ADDR")
        .unwrap_or_else(|_| "0.0.0.0:8081".to_string())
        .parse()?;

    let listener = tokio::net::TcpListener::bind(addr).await?;
    tracing::info!(%addr, "zk-service listening");

    axum::serve(listener, api::router())
        .with_graceful_shutdown(shutdown_signal())
        .await?;

    Ok(())
}

async fn shutdown_signal() {
    let _ = signal::ctrl_c().await;
    tracing::info!("zk-service shutting down");
}
