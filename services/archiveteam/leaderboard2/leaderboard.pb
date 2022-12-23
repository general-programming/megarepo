syntax = "proto3";
package archiveteam_tracker;

option go_package = "github.com/general-programming/services/archiveteam/leaderboard2/fastpb_gen";

message WarriorMeta {
  string user_agent = 1;
  string version = 2;
}

message TrackerStats {
  map<string, double> values = 1;
  map<string, double> queues = 2;
}

message TrackerEvent {
  string project = 1;
  string downloader = 2;
  string log_channel = 3;
  double timestamp = 4 [json_name="ts"];

  uint64 bytes = 10;
  float size_mb = 11 [json_name="megabytes"];
  bool valid = 12;
  bool is_duplicate = 13 [json_name="is_duplicate"];

  string item = 20;
  repeated string items = 21;
  repeated string move_items = 22 [json_name="move_items"];
  repeated double item_rtts = 23 [json_name="item_rtts"];

  string WarriorUserAgent = 30 [json_name="user_agent"];
  string WarriorVersion = 31 [json_name="version"];

  map<string, double> queue_stats = 40 [json_name="queuestats"];
  map<string, double> counts = 41 [json_name="counts"];
  TrackerStats tracker_stats = 42 [json_name="stats"];
  map<string, uint64> domain_bytes = 43 [json_name="domain_bytes"];
}
