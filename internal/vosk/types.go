package vosk

import "encoding/json"

type voskResult struct {
	Text string `json:"text"`
}

func parseResultText(jsonStr string) string {
	var result voskResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return ""
	}
	return result.Text
}
