namespace rs infra.bigbiglarge.scaler
namespace go infra.bigbiglarge.scaler
namespace py infra.bigbiglarge.scaler

struct Server {
    // Location + Metadata
    1: required i64 id,
    2: required string provider,
    3: required string region,
    4: required list<string> tags,
    5: required i64 created, // timestamp in epoch seconds

    // Specs
    10: optional i64 cpu, // CPU cores
    11: optional i64 memory, // MBs of RAM
    12: optional i64 disk,  // GBs of disk
    13: optional string server_type,

    // Networking
    20: optional string ip4, // IPv4 address
    21: optional string ip6, // IPv6 address
}

// GetServersRequest allows filtering by provider, region, and/or tags.
struct GetServersRequest {
    1: optional string provider,
    2: optional string region,
    3: optional list<string> tags,
}

struct GetServersResponse {
    1: required list<Server> servers,
}

service ScalerService {
    GetServersResponse GetServers (1: GetServersRequest req),
}
