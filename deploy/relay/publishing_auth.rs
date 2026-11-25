// Mitemau publishing authorization for the pinned draft-22 Relay.
use super::session::client::MOQTClient;
use moqtail::model::{
  common::tuple::Tuple,
  control::setup::Setup,
  error::TerminationCode,
  parameter::{constant::TokenAliasType, setup_option::SetupOption},
};
use std::{
  sync::{Arc, LazyLock},
  time::Duration,
};
use tokio::time::Instant;

const LEASE: Duration = Duration::from_secs(15);
const REFRESH: Duration = Duration::from_secs(5);

struct Authority {
  url: reqwest::Url,
  key: String,
  http: reqwest::Client,
}

static AUTHORITY: LazyLock<Authority> = LazyLock::new(|| {
  let url = reqwest::Url::parse(&std::env::var("RELAY_AUTH_URL").expect("RELAY_AUTH_URL required"))
    .expect("Invalid RELAY_AUTH_URL");
  assert!(
    url.scheme() == "https"
      || (url.scheme() == "http"
        && matches!(url.host_str(), Some("127.0.0.1" | "[::1]" | "localhost"))),
    "RELAY_AUTH_URL requires HTTPS or loopback HTTP"
  );
  assert!(
    url.username().is_empty() && url.password().is_none() && url.query().is_none(),
    "RELAY_AUTH_URL must not contain credentials or query parameters"
  );
  let key = std::env::var("RELAY_AUTH_KEY").expect("RELAY_AUTH_KEY required");
  assert!(
    key.len() == 64 && key.bytes().all(|c| c.is_ascii_hexdigit()),
    "Invalid RELAY_AUTH_KEY"
  );
  Authority {
    url,
    key: key.to_lowercase(),
    http: reqwest::Client::builder()
      .redirect(reqwest::redirect::Policy::none())
      .timeout(Duration::from_secs(2))
      .build()
      .expect("HTTP client"),
  }
});

pub(super) fn validate_config() {
  LazyLock::force(&AUTHORITY);
}

pub(crate) struct Grant {
  token: String,
  namespace: Tuple,
  deadline: Instant,
}

impl std::fmt::Debug for Grant {
  fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
    f.debug_struct("Grant")
      .field("namespace", &self.namespace)
      .field("deadline", &self.deadline)
      .finish_non_exhaustive()
  }
}

impl Grant {
  pub(crate) fn active(&self) -> bool {
    Instant::now() < self.deadline
  }
  pub(crate) fn permits(&self, namespace: &Tuple) -> bool {
    Instant::now() < self.deadline && self.namespace == *namespace
  }
}

enum Check {
  Allowed(Grant),
  Denied,
  Unavailable,
}

async fn check(token: &str) -> Check {
  // Anchor the deadline before the request, so a delayed response cannot extend a lease.
  let started = Instant::now();
  let response = AUTHORITY
    .http
    .post(AUTHORITY.url.clone())
    .bearer_auth(&AUTHORITY.key)
    .json(&serde_json::json!({"token": token}))
    .send()
    .await;
  let Ok(response) = response else {
    return Check::Unavailable;
  };
  if response.status() == reqwest::StatusCode::FORBIDDEN
    || response.status() == reqwest::StatusCode::UNAUTHORIZED
  {
    return Check::Denied;
  }
  if !response.status().is_success() {
    return Check::Unavailable;
  }
  let Ok(body) = response.json::<serde_json::Value>().await else {
    return Check::Unavailable;
  };
  let Some(namespace) = body["namespace"].as_str() else {
    return Check::Unavailable;
  };
  if body["leaseSeconds"].as_u64() != Some(15) || namespace.len() > 512 {
    return Check::Unavailable;
  }
  Check::Allowed(Grant {
    token: token.to_owned(),
    namespace: Tuple::from_utf8_path(namespace),
    deadline: started + LEASE,
  })
}

pub(super) async fn authenticate(setup: &Setup) -> Result<Option<Grant>, TerminationCode> {
  let mut token = None;
  for option in &setup.setup_options {
    if let Ok(SetupOption::AuthorizationToken { token: auth }) = SetupOption::deserialize(option) {
      if token.is_some()
        || auth.token_type != Some(0)
        || !matches!(auth.alias_type, n if n == TokenAliasType::Register as u64 || n == TokenAliasType::UseValue as u64)
      {
        return Err(TerminationCode::Unauthorized);
      }
      let bytes = auth.token_value.ok_or(TerminationCode::Unauthorized)?;
      if bytes.len() != 64 || !bytes.iter().all(u8::is_ascii_hexdigit) {
        return Err(TerminationCode::Unauthorized);
      }
      token = Some(String::from_utf8(bytes.to_vec()).map_err(|_| TerminationCode::Unauthorized)?);
    }
  }
  let Some(token) = token else { return Ok(None) };
  match check(&token).await {
    Check::Allowed(grant) => Ok(Some(grant)),
    _ => Err(TerminationCode::Unauthorized),
  }
}

pub(super) async fn monitor(client: Arc<MOQTClient>) {
  let (token, namespace, mut deadline) = {
    let guard = client.publishing_grant.read().unwrap();
    let Some(grant) = guard.as_ref() else { return };
    (grant.token.clone(), grant.namespace.clone(), grant.deadline)
  };
  let mut refresh = tokio::time::interval_at(Instant::now() + REFRESH, REFRESH);
  refresh.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
  loop {
    tokio::select! {
      biased;
      _ = client.connection.closed() => return,
      _ = tokio::time::sleep_until(deadline) => break,
      _ = refresh.tick() => {
        // Keep the hard deadline active while an authority request is in flight.
        let result = tokio::select! {
          biased;
          _ = client.connection.closed() => return,
          _ = tokio::time::sleep_until(deadline) => break,
          result = check(&token) => result,
        };
        match result {
          Check::Allowed(grant) if grant.namespace == namespace => {
            deadline = grant.deadline;
            *client.publishing_grant.write().unwrap() = Some(grant);
          }
          Check::Unavailable => {}
          _ => break,
        }
      }
    }
  }
  *client.publishing_grant.write().unwrap() = None;
  client.connection.close(
    TerminationCode::Unauthorized.to_u32(),
    b"Publishing permission expired",
  );
}

#[cfg(test)]
mod tests {
  use super::*;

  #[test]
  fn exact_scope_and_expiration() {
    let namespace = Tuple::from_utf8_path("mitemau/streams/a/publishers/b/c");
    let mut grant = Grant {
      token: "private-test-credential".into(),
      namespace: namespace.clone(),
      deadline: Instant::now() + LEASE,
    };
    assert!(grant.permits(&namespace));
    assert!(!format!("{grant:?}").contains("private-test-credential"));
    let joined_field = Tuple {
      fields: vec![moqtail::model::common::tuple::TupleField::from_utf8(
        "mitemau/streams/a/publishers/b/c",
      )],
    };
    assert!(!grant.permits(&joined_field));
    assert!(!grant.permits(&Tuple::from_utf8_path("mitemau/streams/a/publishers/b")));
    assert!(!grant.permits(&Tuple::from_utf8_path(
      "mitemau/streams/a/publishers/b/c/extra"
    )));
    assert!(!grant.permits(&Tuple::from_utf8_path(
      "mitemau/streams/a/publishers/other/c"
    )));
    grant.deadline = Instant::now();
    assert!(!grant.permits(&namespace));
  }
}
