//! Mitemau's server-owned comment Publisher, using the pinned MOQtail draft-22 API.
//! The Go API is authoritative; this process never accepts browser submissions.
use anyhow::{Context, Result, bail};
use bytes::Bytes;
use moqtail::model::{
  common::{reason_phrase::ReasonPhrase, tuple::Tuple},
  control::{
    control_message::{ControlMessage, ControlMessageTrait},
    publish_namespace::PublishNamespace,
    request_error::RequestError,
    setup::{Setup, SetupSender},
    subscribe_ok::SubscribeOk,
  },
  data::{
    constant::DEFAULT_PUBLISHER_PRIORITY, object::Object, subgroup_header::SubgroupHeader,
    subgroup_object::SubgroupObject,
  },
  error::RequestErrorCode,
  parameter::{authorization_token::AuthorizationToken, setup_option::SetupOption},
};
use moqtail::transport::{
  connection::TransportConnection,
  control_stream_handler::ControlStreamHandler,
  data_stream_handler::{HeaderInfo, SendDataStream},
};
use serde_json::{Value, json};
use std::{
  collections::HashMap,
  io::Read,
  sync::{
    Arc,
    atomic::{AtomicU64, Ordering},
  },
  time::Duration,
};
use tokio::sync::Mutex;
use wtransport::{quinn, tls};

#[derive(Clone)]
struct Api {
  http: reqwest::Client,
  base: reqwest::Url,
  key: String,
}
impl Api {
  async fn request(&self, path: &str, body: Option<Value>) -> Result<Option<Value>> {
    let mut url = self.base.clone();
    url.set_path(path);
    let request = if let Some(body) = body {
      self.http.post(url).json(&body)
    } else {
      self.http.get(url)
    };
    let response = request.bearer_auth(&self.key).send().await?;
    if response.status() == reqwest::StatusCode::NOT_FOUND
      || response.status() == reqwest::StatusCode::CONFLICT
    {
      return Ok(None);
    }
    if !response.status().is_success() {
      bail!("comment API temporarily unavailable")
    }
    Ok(Some(response.json().await?))
  }
}
struct Connected {
  connection: Arc<TransportConnection>,
  _endpoint: quinn::Endpoint,
  _control: ControlStreamHandler,
}
impl Drop for Connected {
  fn drop(&mut self) {
    self.connection.close(0, b"Comment publisher stopped");
  }
}
async fn connect(token: &str) -> Result<Connected> {
  let url = reqwest::Url::parse(
    &std::env::var("COMMENT_RELAY_URL").unwrap_or("moqt://relay-test.supurazako.com:4433".into()),
  )?;
  if url.scheme() != "moqt" {
    bail!("COMMENT_RELAY_URL must use moqt://")
  }
  let host = url.host_str().context("Relay host missing")?;
  let port = url.port().unwrap_or(4433);
  let address = if let Ok(address) = std::env::var("COMMENT_RELAY_ADDRESS") {
    address.parse()?
  } else {
    tokio::net::lookup_host((host, port))
      .await?
      .next()
      .context("Relay DNS failed")?
  };
  let mut tls_config =
    tls::client::build_default_tls_config(Arc::new(tls::build_native_cert_store()), None);
  tls_config.alpn_protocols = vec![b"moqt-22".to_vec()];
  let crypto = quinn::crypto::rustls::QuicClientConfig::try_from(tls_config)?;
  let mut transport = quinn::TransportConfig::default();
  transport.keep_alive_interval(Some(Duration::from_secs(3)));
  transport.max_idle_timeout(Some(Duration::from_secs(7).try_into()?));
  let mut config = quinn::ClientConfig::new(Arc::new(crypto));
  config.transport_config(Arc::new(transport));
  let endpoint = quinn::Endpoint::client(if address.is_ipv6() {
    "[::]:0".parse()?
  } else {
    "0.0.0.0:0".parse()?
  })?;
  let connection = Arc::new(TransportConnection::Quic(
    endpoint.connect_with(config, address, host)?.await?,
  ));
  let options = vec![
    SetupOption::new_authority(format!("{host}:{port}")).try_into()?,
    SetupOption::new_path("/".into()).try_into()?,
    SetupOption::new_auth_token(AuthorizationToken::new_register(
      1,
      0,
      Bytes::copy_from_slice(token.as_bytes()),
    ))
    .try_into()?,
  ];
  let setup = Setup::new(options);
  let mut send = connection.open_uni().await?;
  send.write_all(&setup.serialize()?).await?;
  let receive = connection.accept_uni().await?;
  let mut control = ControlStreamHandler::new(send, receive);
  let setup = control
    .read_setup()
    .await
    .map_err(|_| anyhow::anyhow!("Relay rejected comment SETUP"))?;
  setup
    .validate_incoming(SetupSender::Server, connection.kind())
    .map_err(|_| anyhow::anyhow!("Invalid server SETUP"))?;
  Ok(Connected {
    connection,
    _endpoint: endpoint,
    _control: control,
  })
}
async fn send_snapshots(
  connection: Arc<TransportConnection>,
  api: Api,
  stream: String,
  namespace: String,
  alias: u64,
) -> Result<()> {
  let mut version = None;
  loop {
    let Some(snapshot) = api
      .request(&format!("/internal/comments/{stream}"), None)
      .await?
    else {
      return Ok(());
    };
    if snapshot["namespace"].as_str() != Some(&namespace) {
      bail!("Comment publication was superseded")
    }
    let current = snapshot["version"]
      .as_str()
      .context("Comment version missing")?
      .parse::<u64>()?;
    if current >= (1u64 << 62) {
      bail!("Comment version exceeds MOQT location range")
    }
    if version != Some(current) {
      let payload = Bytes::from(serde_json::to_vec(&snapshot)?);
      let stream = Arc::new(Mutex::new(connection.open_uni().await?));
      let header =
        SubgroupHeader::new_with_explicit_id(alias, current, 0, Some(128), true, true, true);
      let mut handler =
        SendDataStream::new(stream.clone(), HeaderInfo::Subgroup { header }).await?;
      let object = Object::try_from_subgroup(
        SubgroupObject {
          object_id: 0,
          properties: Some(vec![]),
          object_status: None,
          payload: Some(payload),
        },
        alias,
        current,
        Some(0),
        Some(128),
        DEFAULT_PUBLISHER_PRIORITY,
      )?;
      handler.send_object(&object, None).await?;
      stream.lock().await.finish().await?;
      version = Some(current);
    }
    tokio::time::sleep(Duration::from_secs(1)).await;
  }
}
async fn serve_requests(
  connection: Arc<TransportConnection>,
  api: Api,
  stream: String,
  namespace: String,
) -> Result<()> {
  let aliases = Arc::new(AtomicU64::new(1));
  loop {
    let (send, recv) = connection.accept_bi().await?;
    let conn = connection.clone();
    let api = api.clone();
    let id = stream.clone();
    let ns = namespace.clone();
    let aliases = aliases.clone();
    tokio::spawn(async move {
      let mut request = ControlStreamHandler::new(send, recv);
      let Ok(ControlMessage::Subscribe(sub)) = request.next_message().await else {
        let _ = request
          .send_impl(&RequestError::new(
            RequestErrorCode::DoesNotExist,
            0,
            ReasonPhrase::try_new("Only live comment subscriptions are supported".into()).unwrap(),
          ))
          .await;
        return;
      };
      if sub.track_namespace != Tuple::from_utf8_path(&ns)
        || sub.track_name.as_bytes() != b"comments"
      {
        let _ = request
          .send_impl(&RequestError::new(
            RequestErrorCode::DoesNotExist,
            0,
            ReasonPhrase::try_new("Unknown comment track".into()).unwrap(),
          ))
          .await;
        return;
      }
      let alias = aliases.fetch_add(1, Ordering::Relaxed);
      if request
        .send_impl(&SubscribeOk::new(alias, vec![], vec![]))
        .await
        .is_err()
      {
        return;
      }
      tokio::select! {
       _=send_snapshots(conn,api,id,ns,alias)=>{},
       _=request.next_message()=>{},
      }
    });
  }
}
async fn publish_stream(api: Api, id: String) -> Result<()> {
  let mut random = [0u8; 16];
  std::fs::File::open("/dev/urandom")?.read_exact(&mut random)?;
  let session = hex::encode(random);
  let Some(prepared) = api
    .request(
      &format!("/internal/comments/{id}/prepare"),
      Some(json!({"sessionId":session})),
    )
    .await?
  else {
    return Ok(());
  };
  let token = prepared["token"]
    .as_str()
    .context("Comment credential missing")?;
  let namespace = prepared["namespace"]
    .as_str()
    .context("Comment namespace missing")?
    .to_string();
  let connected = tokio::time::timeout(Duration::from_secs(8), connect(token)).await??;
  let connection = connected.connection.clone();
  let (send, recv) = connection.open_bi().await?;
  let mut announcement = ControlStreamHandler::new(send, recv);
  announcement
    .send(&ControlMessage::PublishNamespace(Box::new(
      PublishNamespace::new(0, Tuple::from_utf8_path(&namespace), &[]),
    )))
    .await?;
  if !matches!(
    tokio::time::timeout(Duration::from_secs(5), announcement.next_message()).await?,
    Ok(ControlMessage::RequestOk(_))
  ) {
    bail!("Relay rejected comment namespace")
  }
  let serving = tokio::spawn(serve_requests(
    connection.clone(),
    api.clone(),
    id.clone(),
    namespace,
  ));
  let activated = api
    .request(
      &format!("/internal/comments/{id}/activate"),
      Some(json!({"sessionId":session})),
    )
    .await;
  if !matches!(activated, Ok(Some(_))) {
    serving.abort();
    bail!("Comment activation failed")
  }
  connection.closed().await;
  serving.abort();
  drop(announcement);
  drop(connected);
  Ok(())
}
#[tokio::main]
async fn main() -> Result<()> {
  let base = reqwest::Url::parse(&std::env::var("RELAY_AUTH_URL")?)?;
  if !(base.scheme() == "https"
    || (base.scheme() == "http"
      && matches!(base.host_str(), Some("127.0.0.1" | "localhost" | "[::1]"))))
    || base.username() != ""
    || base.password().is_some()
    || base.query().is_some()
  {
    bail!("Comment API requires HTTPS or loopback HTTP without URL credentials")
  }
  let key = std::env::var("RELAY_AUTH_KEY")?.to_lowercase();
  if key.len() != 64 || !key.bytes().all(|b| b.is_ascii_hexdigit()) {
    bail!("Invalid service key")
  }
  let api = Api {
    http: reqwest::Client::builder()
      .timeout(Duration::from_secs(2))
      .redirect(reqwest::redirect::Policy::none())
      .build()?,
    base,
    key,
  };
  let mut workers: HashMap<String, tokio::task::JoinHandle<()>> = HashMap::new();
  loop {
    if let Ok(Some(value)) = api.request("/internal/comment-streams", None).await {
      if let Some(ids) = value.as_array() {
        workers.retain(|id, worker| {
          let live = ids.iter().any(|v| v.as_str() == Some(id));
          if !live {
            worker.abort()
          };
          live
        });
        for id in ids.iter().filter_map(Value::as_str) {
          if workers.get(id).is_some_and(|task| !task.is_finished()) {
            continue;
          }
          let api = api.clone();
          let id = id.to_owned();
          workers.insert(
            id.clone(),
            tokio::spawn(async move {
              if publish_stream(api, id).await.is_err() {
                eprintln!("Comment publisher disconnected; retrying");
              }
            }),
          );
        }
      }
    }
    tokio::select! {_=tokio::time::sleep(Duration::from_secs(2))=>{},_=tokio::signal::ctrl_c()=>{for worker in workers.values(){worker.abort()};return Ok(())}}
  }
}
