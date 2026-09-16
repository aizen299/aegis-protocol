use std::sync::Arc;

use axum::{
    extract::{Json, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post},
    Router,
};

use crate::prover::{self, ProverConfig, ProverError};
use crate::types::{ErrorResponse, ProveRequest};

pub fn router(config: ProverConfig) -> Router {
    Router::new()
        .route("/health", get(health))
        .route("/ready", get(ready))
        .route("/prove/vault-membership", post(prove_vault_membership))
        .with_state(Arc::new(config))
}

async fn health() -> impl IntoResponse {
    (StatusCode::OK, Json(serde_json::json!({ "status": "ok" })))
}

/// Readiness is not liveness: the process can serve /health long before it can prove anything, and
/// reporting ready without the circuit would route traffic to a service that fails every request.
async fn ready(State(config): State<Arc<ProverConfig>>) -> Response {
    if config.circuit_dir.join("Nargo.toml").exists() {
        return (
            StatusCode::OK,
            Json(serde_json::json!({ "status": "ready" })),
        )
            .into_response();
    }
    (
        StatusCode::SERVICE_UNAVAILABLE,
        Json(ErrorResponse {
            error: "the circuit is not available".into(),
            code: "CIRCUIT_UNAVAILABLE",
        }),
    )
        .into_response()
}

/// The request body carries private inputs. It is never logged, is zeroized once proving returns,
/// and cannot be serialized into a response — `PrivateInputs` has no `Serialize` impl.
async fn prove_vault_membership(
    State(config): State<Arc<ProverConfig>>,
    Json(req): Json<ProveRequest>,
) -> Response {
    // Blocking work off the async runtime: proving takes seconds and would otherwise stall the
    // executor for every other request.
    let result =
        tokio::task::spawn_blocking(move || prover::prove_vault_membership(req, &config)).await;

    match result {
        Ok(Ok(response)) => (StatusCode::OK, Json(response)).into_response(),
        Ok(Err(err)) => error_response(err),
        Err(_) => error_response(ProverError::ProvingFailed),
    }
}

fn error_response(err: ProverError) -> Response {
    let (status, code) = match err {
        ProverError::InvalidInput(_) => (StatusCode::BAD_REQUEST, "INVALID_INPUT"),
        ProverError::ToolchainUnavailable(_) => {
            (StatusCode::SERVICE_UNAVAILABLE, "TOOLCHAIN_UNAVAILABLE")
        }
        ProverError::ProvingFailed => (StatusCode::UNPROCESSABLE_ENTITY, "PROVING_FAILED"),
        ProverError::MalformedOutput => (StatusCode::INTERNAL_SERVER_ERROR, "MALFORMED_OUTPUT"),
    };

    // InvalidInput carries a caller-supplied label only; every other variant's Display is fixed
    // text, so nothing derived from a secret reaches the body.
    (
        status,
        Json(ErrorResponse {
            error: err.to_string(),
            code,
        }),
    )
        .into_response()
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;
    use axum::http::Request;
    use http_body_util::BodyExt;
    use tower::ServiceExt;

    const SECRET: &str = "424242";

    fn config_without_a_circuit() -> ProverConfig {
        ProverConfig {
            circuit_dir: "/nonexistent/circuit".into(),
            nargo: "nargo".into(),
            bb: "bb".into(),
        }
    }

    fn body(secret: &str, gate: &str) -> String {
        format!(
            r#"{{"merkleRoot":"12345","nullifierHash":"67890","actionId":"7","chainId":"31337",
                 "gate":"{gate}","submitter":"205","secret":"{secret}","pathElements":["1000","1001"],
                 "pathIndices":[0,1]}}"#
        )
    }

    async fn post(config: ProverConfig, payload: String) -> (StatusCode, String) {
        let response = router(config)
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/prove/vault-membership")
                    .header("content-type", "application/json")
                    .body(Body::from(payload))
                    .unwrap(),
            )
            .await
            .unwrap();

        let status = response.status();
        let bytes = response.into_body().collect().await.unwrap().to_bytes();
        (status, String::from_utf8(bytes.to_vec()).unwrap())
    }

    #[tokio::test]
    async fn health_is_always_ok() {
        let response = router(config_without_a_circuit())
            .oneshot(
                Request::builder()
                    .uri("/health")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();

        assert_eq!(response.status(), StatusCode::OK);
    }

    /// Readiness is not liveness. Reporting ready without a circuit would route traffic to a
    /// service that fails every request it receives.
    #[tokio::test]
    async fn readiness_fails_without_a_circuit() {
        let response = router(config_without_a_circuit())
            .oneshot(
                Request::builder()
                    .uri("/ready")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();

        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    }

    /// A defaulted submitter would be zero, and a proof bound to the zero address is one the gate
    /// can never accept — a silent failure at proving time rather than a loud one at the request.
    #[tokio::test]
    async fn a_request_without_a_submitter_is_refused() {
        let without = r#"{"merkleRoot":"12345","nullifierHash":"67890","actionId":"7",
                          "chainId":"31337","gate":"171","secret":"424242",
                          "pathElements":["1000","1001"],"pathIndices":[0,1]}"#;

        let (status, _) = post(config_without_a_circuit(), without.into()).await;
        assert_ne!(
            status,
            StatusCode::OK,
            "a request with no submitter was accepted"
        );
    }

    #[tokio::test]
    async fn a_public_input_outside_the_field_is_a_bad_request() {
        let (status, body) = post(
            config_without_a_circuit(),
            body(SECRET, crate::types::FIELD_SIZE),
        )
        .await;

        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert!(body.contains("INVALID_INPUT"), "{body}");
    }

    /// The response body is checked against the secret directly. An error that echoed the witness
    /// would leak it through an ordinary 400, which is the failure mode this endpoint exists to
    /// avoid.
    #[tokio::test]
    async fn no_response_body_ever_contains_the_secret() {
        for payload in [
            body(SECRET, crate::types::FIELD_SIZE),
            body(SECRET, "171"),
            body("not-a-number", "171"),
        ] {
            let (_, response) = post(config_without_a_circuit(), payload).await;
            assert!(
                !response.contains(SECRET),
                "a response leaked the secret: {response}"
            );
            assert!(
                !response.contains("not-a-number"),
                "a response echoed a private value: {response}"
            );
        }
    }

    #[tokio::test]
    async fn a_missing_circuit_is_service_unavailable_not_a_bad_request() {
        let (status, body) = post(config_without_a_circuit(), body(SECRET, "171")).await;

        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert!(body.contains("TOOLCHAIN_UNAVAILABLE"), "{body}");
    }

    #[tokio::test]
    async fn a_malformed_body_is_rejected() {
        let (status, _) = post(config_without_a_circuit(), "{".into()).await;
        assert_eq!(status, StatusCode::BAD_REQUEST);
    }
}
