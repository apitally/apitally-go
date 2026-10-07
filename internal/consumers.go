package internal

import (
	"container/list"
	"hash/maphash"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"

	root "github.com/apitally/apitally-go"
)

const (
	consumerUpdateEventName  = "apitally.consumer.update"
	maxConsumerIdentifier    = 128
	maxConsumerName          = 64
	maxConsumerAttributeKey  = 64
	maxConsumerAttributeText = 1_024
	maxConsumerAttributes    = 10
	maxCachedConsumers       = 10_000
)

// requestConsumer is a request's normalized consumer: the identifier for
// span and metric attribution and the metadata patch for a consumer-update
// event.
type requestConsumer struct {
	identifier string
	name       string
	group      string
	// attributes keep insertion order. An empty value deletes the attribute.
	attributes []consumerAttribute
}

type consumerAttribute struct {
	key   string
	value string
}

// mergeConsumer applies c to the request's consumer. A different identifier
// starts over; the same identifier keeps the latest non-empty name and group
// and merges attributes, taken in key order because map order is random.
// Values are copied, because Fiber reuses the memory of request strings.
func mergeConsumer(current *requestConsumer, c root.Consumer) *requestConsumer {
	identifier := strings.Clone(truncateString(strings.TrimSpace(c.Identifier), maxConsumerIdentifier))
	if identifier == "" {
		return current
	}
	merged := &requestConsumer{identifier: identifier}
	if current != nil && current.identifier == identifier {
		*merged = *current
		merged.attributes = slices.Clone(current.attributes)
	}
	if name := truncateString(strings.TrimSpace(c.Name), maxConsumerName); name != "" {
		merged.name = strings.Clone(name)
	}
	if group := truncateString(strings.TrimSpace(c.Group), maxConsumerName); group != "" {
		merged.group = strings.Clone(group)
	}
	attributes := make([]consumerAttribute, 0, len(c.Attributes))
	for key, value := range c.Attributes {
		attributes = append(attributes, consumerAttribute{key: strings.Clone(strings.TrimSpace(key)), value: strings.Clone(strings.TrimSpace(value))})
	}
	slices.SortFunc(attributes, func(a, b consumerAttribute) int { return strings.Compare(a.key, b.key) })
	for _, a := range attributes {
		if a.key == "" || utf8.RuneCountInString(a.key) > maxConsumerAttributeKey || utf8.RuneCountInString(a.value) > maxConsumerAttributeText {
			continue
		}
		if i := slices.IndexFunc(merged.attributes, func(existing consumerAttribute) bool { return existing.key == a.key }); i >= 0 {
			merged.attributes[i].value = a.value
		} else if len(merged.attributes) < maxConsumerAttributes {
			merged.attributes = append(merged.attributes, a)
		}
	}
	return merged
}

func (c *requestConsumer) hasMetadata() bool {
	return c.name != "" || c.group != "" || len(c.attributes) > 0
}

// consumerUpdates suppresses consumer-update events whose payload matches
// the last one emitted for the identifier, keeping hashes of the least
// recently used identifiers up to a limit.
type consumerUpdates struct {
	mu      sync.Mutex
	seed    maphash.Seed
	hashes  map[string]*list.Element
	recency *list.List
}

type consumerHash struct {
	identifier string
	hash       uint64
}

func newConsumerUpdates() *consumerUpdates {
	return &consumerUpdates{seed: maphash.MakeSeed(), hashes: map[string]*list.Element{}, recency: list.New()}
}

// isChanged records the consumer's payload hash and reports whether it
// differs from the last one recorded for its identifier.
func (u *consumerUpdates) isChanged(c *requestConsumer) bool {
	hash := maphash.String(u.seed, c.canonicalPayload())
	u.mu.Lock()
	defer u.mu.Unlock()
	if element, ok := u.hashes[c.identifier]; ok {
		u.recency.MoveToBack(element)
		entry := element.Value.(*consumerHash)
		isChanged := entry.hash != hash
		entry.hash = hash
		return isChanged
	}
	u.hashes[c.identifier] = u.recency.PushBack(&consumerHash{identifier: c.identifier, hash: hash})
	if u.recency.Len() > maxCachedConsumers {
		delete(u.hashes, u.recency.Remove(u.recency.Front()).(*consumerHash).identifier)
	}
	return true
}

// canonicalPayload is independent of attribute order.
func (c *requestConsumer) canonicalPayload() string {
	attributes := slices.Clone(c.attributes)
	slices.SortFunc(attributes, func(a, b consumerAttribute) int { return strings.Compare(a.key, b.key) })
	parts := []string{strconv.Quote(c.name), strconv.Quote(c.group)}
	for _, a := range attributes {
		parts = append(parts, strconv.Quote(a.key)+"="+strconv.Quote(a.value))
	}
	return strings.Join(parts, ",")
}

// consumerUpdateEventBody encodes a deleted attribute as null.
func consumerUpdateEventBody(c *requestConsumer) attribute.Value {
	body := []attribute.KeyValue{attribute.String("identifier", c.identifier)}
	if c.name != "" {
		body = append(body, attribute.String("name", c.name))
	}
	if c.group != "" {
		body = append(body, attribute.String("group", c.group))
	}
	if len(c.attributes) > 0 {
		attrs := make([]attribute.KeyValue, len(c.attributes))
		for i, a := range c.attributes {
			attrs[i] = attribute.KeyValue{Key: attribute.Key(a.key)}
			if a.value != "" {
				attrs[i].Value = attribute.StringValue(a.value)
			}
		}
		body = append(body, attribute.Map("attributes", attrs...))
	}
	return attribute.MapValue(body...)
}
