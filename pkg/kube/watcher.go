package kube

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/giantswarm/kubernetes-event-exporter/v2/pkg/metrics"
)

var startUpTime = time.Now()

type EventHandler func(event *EnhancedEvent)

type EventWatcher struct {
	wg                  sync.WaitGroup
	informers           []cache.SharedInformer
	stopper             chan struct{}
	objectMetadataCache ObjectMetadataProvider
	omitLookup          bool
	fn                  EventHandler
	maxEventAgeSeconds  time.Duration
	metricsStore        *metrics.Store
	dynamicClient       *dynamic.DynamicClient
	clientset           *kubernetes.Clientset
	watchKinds          map[string]struct{}
}

func NewEventWatcher(config *rest.Config, namespace string, MaxEventAgeSeconds int64, metricsStore *metrics.Store, fn EventHandler, omitLookup bool, cacheSize int, watchKinds []string, watchReasons []string) *EventWatcher {
	clientset := kubernetes.NewForConfigOrDie(config)
	informerList := make([]cache.SharedInformer, 0)

	if len(watchReasons) == 0 {
		// Default behavior: one informer, no reason filtering
		factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithNamespace(namespace))
		informerList = append(informerList, factory.Core().V1().Events().Informer())
	} else {
		// Create one informer per reason
		for _, reason := range watchReasons {
			// Create a new variable for the closure to capture.
			r := reason
			tweakListOptions := func(options *metav1.ListOptions) {
				options.FieldSelector = fields.OneTermEqualSelector("reason", r).String()
			}
			factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithNamespace(namespace), informers.WithTweakListOptions(tweakListOptions))
			informerList = append(informerList, factory.Core().V1().Events().Informer())
		}
	}

	watcher := &EventWatcher{
		informers:           informerList,
		stopper:             make(chan struct{}),
		objectMetadataCache: NewObjectMetadataProvider(cacheSize),
		omitLookup:          omitLookup,
		fn:                  fn,
		maxEventAgeSeconds:  time.Second * time.Duration(MaxEventAgeSeconds),
		metricsStore:        metricsStore,
		dynamicClient:       dynamic.NewForConfigOrDie(config),
		clientset:           clientset,
		watchKinds:          kindsToMap(watchKinds),
	}

	for _, informer := range watcher.informers {
		informer.AddEventHandler(watcher)
		informer.SetWatchErrorHandler(func(r *cache.Reflector, err error) {
			watcher.metricsStore.WatchErrors.Inc()
		})
	}

	return watcher
}

func (e *EventWatcher) OnAdd(obj interface{}) {
	event := obj.(*corev1.Event)
	e.onEvent(event)
}

func (e *EventWatcher) OnUpdate(oldObj, newObj interface{}) {
	// Ignore updates
}

// Ignore events older than the maxEventAgeSeconds
func (e *EventWatcher) isEventDiscarded(event *corev1.Event) bool {
	timestamp := event.LastTimestamp.Time
	if timestamp.IsZero() {
		timestamp = event.EventTime.Time
	}
	eventAge := time.Since(timestamp)
	if eventAge > e.maxEventAgeSeconds {
		// Log discarded events if they were created after the watcher started
		// (to suppres warnings from initial synchrnization)
		if timestamp.After(startUpTime) {
			log.Warn().
				Str("event age", eventAge.String()).
				Str("event namespace", event.Namespace).
				Str("event name", event.Name).
				Msg("Event discarded as being older then maxEventAgeSeconds")
			e.metricsStore.EventsDiscarded.Inc()
		}
		return true
	}
	return false
}

func (e *EventWatcher) onEvent(event *corev1.Event) {
	if e.isEventDiscarded(event) {
		return
	}

	log.Debug().
		Str("msg", event.Message).
		Str("namespace", event.Namespace).
		Str("reason", event.Reason).
		Str("involvedObject", event.InvolvedObject.Name).
		Msg("Received event")

	e.metricsStore.EventsProcessed.Inc()

	ev := &EnhancedEvent{
		Event: *event.DeepCopy(),
	}
	ev.Event.ManagedFields = nil

	if e.omitLookup || !e.shouldLookup(ev) {
		ev.InvolvedObject.ObjectReference = *event.InvolvedObject.DeepCopy()
	} else {
		objectMetadata, err := e.objectMetadataCache.GetObjectMetadata(&event.InvolvedObject, e.clientset, e.dynamicClient, e.metricsStore)
		if err != nil {
			if errors.IsNotFound(err) {
				ev.InvolvedObject.Deleted = true
				log.Warn().Err(err).Msg("Object not found, likely deleted")
			} else if errors.IsForbidden(err) {
				log.Debug().Err(err).Msg("failed to get object metadata, it is forbidden")
			} else {
				log.Error().Err(err).Msg("Failed to get object metadata")
			}
			ev.InvolvedObject.ObjectReference = *event.InvolvedObject.DeepCopy()
		} else {
			ev.InvolvedObject.Labels = objectMetadata.Labels
			ev.InvolvedObject.Annotations = objectMetadata.Annotations
			ev.InvolvedObject.OwnerReferences = objectMetadata.OwnerReferences
			ev.InvolvedObject.ObjectReference = *event.InvolvedObject.DeepCopy()
			ev.InvolvedObject.Deleted = objectMetadata.Deleted
		}
	}

	e.fn(ev)
}

func (e *EventWatcher) OnDelete(obj interface{}) {
	// Ignore deletes
}

func (e *EventWatcher) Start() {
	for _, informer := range e.informers {
		e.wg.Add(1)
		go func(i cache.SharedInformer) {
			defer e.wg.Done()
			i.Run(e.stopper)
		}(informer)
	}
}

func (e *EventWatcher) Stop() {
	close(e.stopper)
	e.wg.Wait()
}

func (e *EventWatcher) setStartUpTime(time time.Time) {
	startUpTime = time
}

func (e *EventWatcher) shouldLookup(event *EnhancedEvent) bool {
	if len(e.watchKinds) == 0 {
		return true
	}

	for kind := range e.watchKinds {
		matched, _ := regexp.MatchString(kind, event.InvolvedObject.Kind)
		if matched {
			return true
		}
	}

	return false
}

func kindsToMap(kinds []string) map[string]struct{} {
	if len(kinds) == 0 {
		return nil
	}

	m := make(map[string]struct{})
	for _, k := range kinds {
		for _, s := range strings.Split(k, "|") {
			m[s] = struct{}{}
		}
	}
	return m
}
