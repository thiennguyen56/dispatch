package application

type InputSubmit struct {
	URL     string            `json:"url"`
	Payload string            `json:"payload"`
	Headers map[string]string `json:"headers"`
}
