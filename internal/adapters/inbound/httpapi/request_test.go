package httpapi

import "testing"

func TestSubmitDeliveriesIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input *submitDeliveries
		want  bool
	}{
		{
			name: "valid HTTPS request",
			input: &submitDeliveries{
				URL:     "https://hooks.example.com/deliveries",
				Payload: `{"event":"delivery.created"}`,
				Headers: map[string]string{"Authorization": "Bearer token"},
			},
			want: true,
		},
		{
			name:  "empty payload and headers are allowed",
			input: &submitDeliveries{URL: "http://localhost:8081/webhook"},
			want:  true,
		},
		{
			name:  "nil request",
			input: nil,
		},
		{
			name:  "missing URL",
			input: &submitDeliveries{},
		},
		{
			name:  "relative URL",
			input: &submitDeliveries{URL: "/deliveries"},
		},
		{
			name:  "unsupported URL scheme",
			input: &submitDeliveries{URL: "ftp://example.com/deliveries"},
		},
		{
			name: "invalid header name",
			input: &submitDeliveries{
				URL:     "https://example.com",
				Headers: map[string]string{"Bad Header": "value"},
			},
		},
		{
			name: "header newline injection",
			input: &submitDeliveries{
				URL:     "https://example.com",
				Headers: map[string]string{"X-Request-ID": "one\r\ntwo"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.input.IsValid()
			if got := err == nil; got != test.want {
				t.Errorf("IsValid() error = %v, want valid = %t", err, test.want)
			}
		})
	}
}
