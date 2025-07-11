package exporter

import (
	"github.com/giantswarm/kubernetes-event-exporter/v2/pkg/kube"
	"github.com/giantswarm/kubernetes-event-exporter/v2/pkg/sinks"
)

// ReceiverRegistry registers a receiver with the appropriate sink
type ReceiverRegistry interface {
	SendEvent(string, *kube.EnhancedEvent)
	Register(string, sinks.Sink)
	Close()
}
