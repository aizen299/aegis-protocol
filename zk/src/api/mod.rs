use axum::{
    extract::Json,
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post},
    Router,
};

use crate::prover::{self, ProverError};
use crate::types::{ErrorResponse, ProveRequest};

pub fn router() -> Router {
    Router::new()
        .route("/health", get(health))
        .route("/prove/vault-membership", post(prove_vault_membership))
}

async fn health() -> impl IntoResponse {
    (StatusCode::OK, Json(serde_json::json!({ "status": "ok" })))
}

/// The request body carries private inputs. It is never logged and is dropped — and zeroized —
/// before the response is written.
async fn prove_vault_membership(Json(req): Json<ProveRequest>) -> Response {
    match prover::prove_vault_membership(req) {
        Ok(res) => (StatusCode::OK, Json(res)).into_response(),
        Err(err) => error_response(err),
    }
}

fn error_response(err: ProverError) -> Response {
    let (status, code) = match err {
        ProverError::NotImplemented => (StatusCode::NOT_IMPLEMENTED, "NOT_IMPLEMENTED"),
    };
    (
        status,
        Json(ErrorResponse {
            error: err.to_string(),
            code,
        }),
    )
        .into_response()
}
