package exporter

import (
	"github.com/rs/zerolog/log"

	"github.com/giantswarm/kubernetes-event-exporter/pkg/kube"
)

// Route allows using rules to drop events or match events to specific receivers.
// It also allows using routes recursively for complex route building to fit
// most of the needs
type Route struct {
	Drop   []Rule
	Match  []Rule
	Routes []Route
}

func (r *Route) ProcessEvent(ev *kube.EnhancedEvent, registry ReceiverRegistry) {
	// First determine whether we will drop the event: If any of the drop is matched, we break the loop
	for _, v := range r.Drop {
		if v.MatchesEvent(ev) {
			return
		}
	}

	// It has match rules, it should go to the matchers
	matchesAll := true
	for _, rule := range r.Match {
		if rule.MatchesEvent(ev) {
			if rule.Receiver != "" {
				log.Info().
					Str("receiver", rule.Receiver).
					Str("kind", ev.InvolvedObject.Kind).
					Str("name", ev.InvolvedObject.Name).
					Str("namespace", ev.Namespace).
					Str("reason", ev.Reason).
					Msg("Forwarding event to receiver")
				registry.SendEvent(rule.Receiver, ev)
				// Send the event down the hole
			}
		} else {
			matchesAll = false
		}
	}

	// If all matches are satisfied, we can send them down to the rabbit hole
	if matchesAll {
		for _, subRoute := range r.Routes {
			subRoute.ProcessEvent(ev, registry)
		}
	}
}
