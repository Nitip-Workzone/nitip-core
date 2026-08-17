package notification

import (
	"encoding/json"

	"github.com/google/uuid"
)

type jobJSON struct {
	UserID     string            `json:"user_id"`
	Token      string            `json:"token,omitempty"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Type       string            `json:"type"`
	Data       map[string]string `json:"data,omitempty"`
	CollapseID string            `json:"collapse_id,omitempty"`
	Priority   JobPriority       `json:"priority,omitempty"`
	Retry      int               `json:"retry,omitempty"`
}

func MarshalJobJSON(job Job) (string, error) {
	j := jobJSON{
		UserID:     job.UserID.String(),
		Token:      job.Token,
		Title:      job.Title,
		Body:       job.Body,
		Type:       job.Type,
		Data:       job.Data,
		CollapseID: job.CollapseID,
		Priority:   job.Priority,
		Retry:      job.Retry,
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func UnmarshalJobJSON(s string) (Job, error) {
	var j jobJSON
	if err := json.Unmarshal([]byte(s), &j); err != nil {
		return Job{}, err
	}
	uid, _ := uuid.Parse(j.UserID)
	return Job{
		UserID:     uid,
		Token:      j.Token,
		Title:      j.Title,
		Body:       j.Body,
		Type:       j.Type,
		Data:       j.Data,
		CollapseID: j.CollapseID,
		Priority:   j.Priority,
		Retry:      j.Retry,
	}, nil
}
