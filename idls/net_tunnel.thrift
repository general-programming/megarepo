include "./base.thrift"

namespace rs infra.network.tunneler
namespace go infra.network.tunneler
namespace py infra.network.tunneler

struct Tunnel {
    1: required i64 id,
}

struct AuthData {
    1: required string authkey,
}

struct GetTunnelRequest {
    1: optional string pubkey,

    254: required AuthData auth,
    255: base.Base Base,
}

struct GetTunnelResponse {
    1: required string endpoint,

    255: base.BaseResp BaseResp,
}

service TunnelService {
    GetTunnelResponse GetTunnel (1: GetTunnelRequest req),
}
