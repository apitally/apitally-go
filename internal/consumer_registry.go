package internal

import (
	"strings"

	"github.com/apitally/apitally-go/common"
)

func ConsumerFromStringOrObject(consumer any) *common.ApitallyConsumer {
	switch v := consumer.(type) {
	case string:
		identifier := strings.TrimSpace(v)
		if len(identifier) > 128 {
			identifier = identifier[:128]
		}
		if identifier == "" {
			return nil
		}
		return &common.ApitallyConsumer{Identifier: identifier}
	case *common.ApitallyConsumer:
		if v == nil {
			return nil
		}
		v.Identifier = strings.TrimSpace(v.Identifier)
		if v.Identifier == "" {
			return nil
		}
		if len(v.Identifier) > 128 {
			v.Identifier = v.Identifier[:128]
		}
		if v.Name != nil {
			name := strings.TrimSpace(*v.Name)
			if len(name) > 64 {
				name = name[:64]
			}
			v.Name = &name
		}
		if v.Group != nil {
			group := strings.TrimSpace(*v.Group)
			if len(group) > 64 {
				group = group[:64]
			}
			v.Group = &group
		}
		return v
	default:
		return nil
	}
}

type ConsumerRegistry struct {
	consumers map[string]*common.ApitallyConsumer
	updated   map[string]bool
}

func NewConsumerRegistry() *ConsumerRegistry {
	return &ConsumerRegistry{
		consumers: make(map[string]*common.ApitallyConsumer),
		updated:   make(map[string]bool),
	}
}

func (r *ConsumerRegistry) AddOrUpdateConsumer(consumer *common.ApitallyConsumer) {
	if consumer == nil || (consumer.Name == nil && consumer.Group == nil) {
		return
	}

	existing, exists := r.consumers[consumer.Identifier]
	if !exists {
		r.consumers[consumer.Identifier] = consumer
		r.updated[consumer.Identifier] = true
		return
	}

	if consumer.Name != nil && (existing.Name == nil || *consumer.Name != *existing.Name) {
		existing.Name = consumer.Name
		r.updated[consumer.Identifier] = true
	}
	if consumer.Group != nil && (existing.Group == nil || *consumer.Group != *existing.Group) {
		existing.Group = consumer.Group
		r.updated[consumer.Identifier] = true
	}
}

func (r *ConsumerRegistry) GetAndResetUpdatedConsumers() []*common.ApitallyConsumer {
	data := make([]*common.ApitallyConsumer, 0, len(r.updated))
	for identifier := range r.updated {
		if consumer, exists := r.consumers[identifier]; exists {
			data = append(data, consumer)
		}
	}
	r.updated = make(map[string]bool)
	return data
}
