package model

import (
	"encoding/json"
)

type ATTrackerProjects struct {
	WarriorProject string             `json:"auto_project"`
	Projects       []ATTrackerProject `json:"projects"`
}

type ATTrackerProject struct {
	Description     string `json:"description"`
	ProjectName     string `json:"name"`
	Title           string `json:"title"`
	Repository      string `json:"repository"`
	LeaderboardLink string `json:"leaderboard"`
}

type ATTrackerStats struct {
	Values map[string]json.Number `json:"values"`
	Queues map[string]json.Number `json:"queues"`
}

/*
AtTrackerUpdate is a struct representing a single update from the tracker.

Example item:
{
project: "vlive",
bytes: 885096,
stats: {
values: {
reclaim_rate: 0,
reclaim_serve_rate: 0,
rtt_real: 20.578742661713,
rtt: 22.190385926599,
rtt_count_pre: 2351,
filtered_count: 0,
rtt_count: 2130,
item_fail_rate: 0,
item_filter_rate: 0,
done_counter: 2117536,
item_request_serve_rate: 0.9999999999995
},
queues: {
done: 2067253,
unretrievable: 0,
todo:secondary: 0,
claims: 6725227,
todo:redo: 0,
todo:backfeed: 6131683,
todo: 0
}
},
move_items: [
"vphinf:20210806_92/1628244212994m8tOu_JPEG/141032582979787015ea43a6b8-4ddb-44ae-93af-ffac36db759a.jpg"
],
uncached: null,
item_rtts: [
20.350000143051
],
items: [
"vphinf:20210806_92/1628244212994m8tOu_JPEG/141032582979787015ea43a6b8-4ddb-44ae-93af-ffac36db759a.jpg"
],
item: "vphinf:20210806_92/1628244212994m8tOu_JPEG/141032582979787015ea43a6b8-4ddb-44ae-93af-ffac36db759a.jpg",
version: "20221201.01",
user_agent: "ArchiveTeam Warrior/0.10.3 Standalone",
megabytes: 0.84409332275391,
domain_bytes: {
data: 885096
},
log_channel: "vlive-log",
is_duplicate: false,
ts: 1670255906.368,
queuestats: {
done: 2067253,
unretrievable: 0,
todo:secondary: 0,
claims: 6725227,
todo:redo: 0,
todo:backfeed: 6131683,
todo: 0
},
counts: {
ifar: 0,
rcsr: 0,
ifir: 0,
irsr: 0.9999999999995,
out: 6725227,
done: 2117536,
todo: 6131683,
rcr: 0
},
downloader: "datechnoman",
valid: true
}
*/
type ATTrackerUpdate struct {
	Project    string `json:"project"`
	Downloader string `json:"downloader"`
	// Timestamp is a unix timestamp in seconds
	Timestamp json.Number `json:"ts"`

	Bytes       json.Number `json:"bytes"`
	Valid       bool        `json:"valid"`
	IsDuplicate bool        `json:"is_duplicate"`

	Items     []string `json:"items"`
	MoveItems []string `json:"move_items"`

	Item     string        `json:"item"`
	ItemRTTs []json.Number `json:"item_rtts"`

	WarriorUserAgent string `json:"user_agent"`
	WarriorVersion   string `json:"version"`

	SizeMB json.Number `json:"megabytes"`

	QueueStats map[string]json.Number `json:"queuestats"`
	Stats      ATTrackerStats         `json:"stats"`
	Counts     map[string]json.Number `json:"counts"`
}
