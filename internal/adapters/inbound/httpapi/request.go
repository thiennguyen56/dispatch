package httpapi

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type submitDeliveries struct {
	URL     string            `json:"url"`
	Payload string            `json:"payload"`
	Headers map[string]string `json:"headers"`
}

func (sd *submitDeliveries) IsValid() error {
	if sd == nil {
		return errors.New("request is required")
	}

	target, err := url.ParseRequestURI(sd.URL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return errors.New("url must be an absolute URL")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return errors.New("url scheme must be http or https")
	}

	for name, value := range sd.Headers {
		if !isHeaderName(name) {
			return fmt.Errorf("invalid header name %q", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("header %q contains a newline", name)
		}
	}

	return nil
}

func isHeaderName(name string) bool {
	if name == "" {
		return false
	}

	for _, char := range name {
		if ('a' <= char && char <= 'z') ||
			('A' <= char && char <= 'Z') ||
			('0' <= char && char <= '9') ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", char) {
			continue
		}
		return false
	}

	return true
}
