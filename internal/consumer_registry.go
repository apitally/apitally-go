package internal

import (
	"strings"
	"sync"

	"github.com/apitally/apitally-go/common"
)

func validateConsumer(consumer *common.ApitallyConsumer) bool {
	if consumer == nil {
		return false
	}

	consumer.Identifier = strings.TrimSpace(consumer.Identifier)
	if consumer.Identifier == "" {
		return false
	}

	if len(consumer.Identifier) > 128 {
		consumer.Identifier = consumer.Identifier[:128]
	}

	if consumer.Name != "" {
		name := strings.TrimSpace(consumer.Name)
		if len(name) > 64 {
			name = name[:64]
		}
		consumer.Name = name
	}

	if consumer.Group != "" {
		group := strings.TrimSpace(consumer.Group)
		if len(group) > 64 {
			group = group[:64]
		}
		consumer.Group = group
	}

	return true
}

func ConsumerFromStringOrObject(consumer any) *common.ApitallyConsumer {
	switch v := consumer.(type) {
	case string:
		c := &common.ApitallyConsumer{Identifier: v}
		if validateConsumer(c) {
			return c
		}
		return nil
	case common.ApitallyConsumer:
		c := v
		if validateConsumer(&c) {
			return &c
		}
		return nil
	case *common.ApitallyConsumer:
		if validateConsumer(v) {
			return v
		}
		return nil
	default:
		return nil
	}
}

type ConsumerRegistry struct {
	consumers map[string]*common.ApitallyConsumer
	updated   map[string]bool
	mutex     sync.Mutex
}

func NewConsumerRegistry() *ConsumerRegistry {
	return &ConsumerRegistry{
		consumers: make(map[string]*common.ApitallyConsumer),
		updated:   make(map[string]bool),
	}
}

func (r *ConsumerRegistry) AddOrUpdateConsumer(consumer *common.ApitallyConsumer) {
	if consumer == nil || (consumer.Name == "" && consumer.Group == "") {
		return
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	existing, exists := r.consumers[consumer.Identifier]
	if !exists {
		r.consumers[consumer.Identifier] = consumer
		r.updated[consumer.Identifier] = true
		return
	}

	if consumer.Name != "" && (existing.Name == "" || consumer.Name != existing.Name) {
		existing.Name = consumer.Name
		r.updated[consumer.Identifier] = true
	}
	if consumer.Group != "" && (existing.Group == "" || consumer.Group != existing.Group) {
		existing.Group = consumer.Group
		r.updated[consumer.Identifier] = true
	}
}

func (r *ConsumerRegistry) GetAndResetUpdatedConsumers() []*common.ApitallyConsumer {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	data := make([]*common.ApitallyConsumer, 0, len(r.updated))
	for identifier := range r.updated {
		if consumer, exists := r.consumers[identifier]; exists {
			data = append(data, consumer)
		}
	}
	r.updated = make(map[string]bool)
	return data
}
