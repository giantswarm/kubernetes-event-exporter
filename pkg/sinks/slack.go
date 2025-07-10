package sinks

import (
	"context"
	"sort"
	"sync"

	"github.com/giantswarm/kubernetes-event-exporter/pkg/kube"
	"github.com/rs/zerolog/log"
	"github.com/slack-go/slack"
)

type SlackConfig struct {
	Token      string            `yaml:"token"`
	Channel    string            `yaml:"channel"`
	Message    string            `yaml:"message"`
	Color      string            `yaml:"color"`
	Footer     string            `yaml:"footer"`
	Title      string            `yaml:"title"`
	AuthorName string            `yaml:"author_name"`
	Fields     map[string]string `yaml:"fields"`
	// ThreadKey is a template that should evaluate to a unique value for events that should be grouped in a thread.
	ThreadKey string `yaml:"threadKey,omitempty"`
	// CompletionCondition is a template that should evaluate to a non-empty string for the event that is considered to be the completion of a thread.
	CompletionCondition string `yaml:"completionCondition,omitempty"`
	// CompletionEmoji is the emoji to add as a reaction to the first message in a thread when the completion event is received. Defaults to :white_check_mark:
	CompletionEmoji string `yaml:"completionEmoji,omitempty"`
}

type SlackSink struct {
	cfg       *SlackConfig
	client    *slack.Client
	threadTS  map[string]string
	threadMux sync.Mutex
}

func NewSlackSink(cfg *SlackConfig) (Sink, error) {
	if cfg.CompletionEmoji == "" {
		cfg.CompletionEmoji = "white_check_mark"
	}
	return &SlackSink{
		cfg:      cfg,
		client:   slack.New(cfg.Token),
		threadTS: make(map[string]string),
	}, nil
}

func (s *SlackSink) Send(ctx context.Context, ev *kube.EnhancedEvent) error {
	channel, err := GetString(ev, s.cfg.Channel)
	if err != nil {
		return err
	}

	message, err := GetString(ev, s.cfg.Message)
	if err != nil {
		return err
	}

	options := []slack.MsgOption{slack.MsgOptionText(message, true)}
	if s.cfg.Fields != nil {
		fields := make([]slack.AttachmentField, 0)
		for k, v := range s.cfg.Fields {
			fieldText, err := GetString(ev, v)
			if err != nil {
				return err
			}

			fields = append(fields, slack.AttachmentField{
				Title: k,
				Value: fieldText,
				Short: false,
			})
		}

		sort.SliceStable(fields, func(i, j int) bool {
			return fields[i].Title < fields[j].Title
		})

		// make slack attachment
		slackAttachment := slack.Attachment{}
		slackAttachment.Fields = fields
		if s.cfg.AuthorName != "" {
			slackAttachment.AuthorName, err = GetString(ev, s.cfg.AuthorName)
			if err != nil {
				return err
			}
		}
		if s.cfg.Color != "" {
			slackAttachment.Color, err = GetString(ev, s.cfg.Color)
			if err != nil {
				return err
			}
		}
		if s.cfg.Title != "" {
			slackAttachment.Title, err = GetString(ev, s.cfg.Title)
			if err != nil {
				return err
			}
		}
		if s.cfg.Footer != "" {
			slackAttachment.Footer, err = GetString(ev, s.cfg.Footer)
			if err != nil {
				return err
			}
		}

		options = append(options, slack.MsgOptionAttachments(slackAttachment))
	}

	if s.cfg.ThreadKey == "" {
		// No threading configured, send as is.
		_ch, _ts, _text, err := s.client.SendMessageContext(ctx, channel, options...)
		log.Debug().Str("ch", _ch).Str("ts", _ts).Str("text", _text).Err(err).Msg("Slack Response")
		return err
	}

	// Threading is configured
	threadKey, err := GetString(ev, s.cfg.ThreadKey)
	if err != nil {
		log.Warn().Err(err).Str("template", s.cfg.ThreadKey).Msg("Failed to execute threadKey template")
		// Send as a normal message if template fails
		_ch, _ts, _text, err := s.client.SendMessageContext(ctx, channel, options...)
		log.Debug().Str("ch", _ch).Str("ts", _ts).Str("text", _text).Err(err).Msg("Slack Response")
		return err
	}
	if threadKey == "" {
		// Thread key is empty, send as normal message
		_ch, _ts, _text, err := s.client.SendMessageContext(ctx, channel, options...)
		log.Debug().Str("ch", _ch).Str("ts", _ts).Str("text", _text).Err(err).Msg("Slack Response")
		return err
	}

	isCompletion := false
	if s.cfg.CompletionCondition != "" {
		res, err := GetString(ev, s.cfg.CompletionCondition)
		if err != nil {
			log.Warn().Err(err).Str("template", s.cfg.CompletionCondition).Msg("Failed to execute completionCondition template")
		} else if res != "" {
			isCompletion = true
		}
	}

	s.threadMux.Lock()
	defer s.threadMux.Unlock()

	parentTS, found := s.threadTS[threadKey]

	if found {
		options = append(options, slack.MsgOptionTS(parentTS))
	}

	_ch, _ts, _text, err := s.client.SendMessageContext(ctx, channel, options...)
	log.Debug().Str("ch", _ch).Str("ts", _ts).Str("text", _text).Err(err).Msg("Slack Response")
	if err != nil {
		return err
	}

	if isCompletion {
		if found {
			// It's a completion event and we found the parent message.
			// React to the parent message and remove from map.
			itemRef := slack.NewRefToMessage(channel, parentTS)
			err = s.client.AddReactionContext(ctx, s.cfg.CompletionEmoji, itemRef)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to add reaction to slack message")
			}
			delete(s.threadTS, threadKey)
		}
		// If not found, it's a completion event without a start. Just sent as a normal message. Nothing more to do.
	} else {
		if !found {
			// It's a new event, so store its timestamp to start a thread.
			s.threadTS[threadKey] = _ts
		}
		// If found, it's an update. We already posted to the thread. Nothing more to do.
	}

	return nil
}

func (s *SlackSink) Close() {
	// No-op
}
