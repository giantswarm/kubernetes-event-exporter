package sinks

import (
	"context"
	"sort"

	"github.com/rs/zerolog/log"
	"github.com/slack-go/slack"

	"github.com/giantswarm/kubernetes-event-exporter/v2/pkg/kube"
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
	CompletionEmoji string                `yaml:"completionEmoji,omitempty"`
	Cache           *ConfigMapCacheConfig `yaml:"cache,omitempty"`
}

type SlackSink struct {
	cfg    *SlackConfig
	client *slack.Client
	cache  ThreadCache
}

func NewSlackSink(cfg *SlackConfig) (Sink, error) {
	if cfg.CompletionEmoji == "" {
		cfg.CompletionEmoji = "white_check_mark"
	}

	var cache ThreadCache
	if cfg.Cache != nil {
		var err error
		cache, err = NewConfigMapCache(cfg.Cache)
		if err != nil {
			return nil, err
		}
	} else {
		cache = NewInMemoryCache()
	}

	return &SlackSink{
		cfg:    cfg,
		client: slack.New(cfg.Token),
		cache:  cache,
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
		_ch, _ts, _text, err := s.client.SendMessageContext(ctx, channel, options...)
		log.Debug().Str("ch", _ch).Str("ts", _ts).Str("text", _text).Err(err).Msg("Slack Response")
		return err
	}

	threadKey, err := GetString(ev, s.cfg.ThreadKey)
	if err != nil {
		log.Warn().Err(err).Str("template", s.cfg.ThreadKey).Msg("Failed to execute threadKey template")
		_ch, _ts, _text, err := s.client.SendMessageContext(ctx, channel, options...)
		log.Debug().Str("ch", _ch).Str("ts", _ts).Str("text", _text).Err(err).Msg("Slack Response")
		return err
	}
	if threadKey == "" {
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

	parentInfo, found := s.cache.Get(threadKey)

	if found {
		options = append(options, slack.MsgOptionTS(parentInfo.Timestamp))
	}

	_ch, _ts, _text, err := s.client.SendMessageContext(ctx, channel, options...)
	log.Debug().Str("ch", _ch).Str("ts", _ts).Str("text", _text).Err(err).Msg("Slack Response")
	if err != nil {
		return err
	}

	if isCompletion {
		if found {
			// React to the parent message and remove from map.
			itemRef := slack.NewRefToMessage(parentInfo.ChannelID, parentInfo.Timestamp)
			err = s.client.AddReactionContext(ctx, s.cfg.CompletionEmoji, itemRef)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to add reaction to slack message")
			}
			if err := s.cache.Delete(threadKey); err != nil {
				log.Warn().Err(err).Str("threadKey", threadKey).Msg("Failed to delete thread from cache")
			}
		}
	} else {
		if !found {
			err := s.cache.Set(threadKey, threadInfo{
				Timestamp: _ts,
				ChannelID: _ch,
			})
			if err != nil {
				log.Warn().Err(err).Str("threadKey", threadKey).Msg("Failed to set thread in cache")
			}
		}
	}

	return nil
}

func (s *SlackSink) Close() {
	// No-op
}
