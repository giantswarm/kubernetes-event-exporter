package sinks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/giantswarm/kubernetes-event-exporter/v2/pkg/kube"
)

type WebhookConfig struct {
	Endpoint string                 `yaml:"endpoint"`
	TLS      TLS                    `yaml:"tls"`
	Layout   map[string]interface{} `yaml:"layout"`
	Headers  map[string]string      `yaml:"headers"`
}

func NewWebhook(cfg *WebhookConfig) (Sink, error) {
	tlsClientConfig, err := setupTLS(&cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to setup TLS: %w", err)
	}
	return &Webhook{cfg: cfg, transport: &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: tlsClientConfig,
	}}, nil
}

type Webhook struct {
	cfg       *WebhookConfig
	transport *http.Transport
}

func (w *Webhook) Close() {
	w.transport.CloseIdleConnections()
}

func (w *Webhook) Send(ctx context.Context, ev *kube.EnhancedEvent) error {
	reqBody, err := serializeEventWithLayout(w.cfg.Layout, ev)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, w.cfg.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")

	for k, v := range w.cfg.Headers {
		realValue, err := GetString(ev, v)
		if err != nil {
			log.Debug().Err(err).Msgf("parse template failed: %s", v)
			req.Header.Add(k, v)
		} else {
			log.Debug().Msgf("request header: {%s: %s}", k, realValue)
			req.Header.Add(k, realValue)
		}
	}

	client := http.DefaultClient
	client.Transport = w.transport
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return errors.New("not successfull (2xx) response: " + string(body))
	}

	return nil
}
