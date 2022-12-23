package model

type ClientMessage struct {
	Type string                 `json:"type"`
	Args map[string]interface{} `json:"-"`
}
