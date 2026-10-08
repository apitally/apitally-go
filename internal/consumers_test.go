package internal

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestConsumerIsNormalizedAndMerged(t *testing.T) {
	attributes := map[string]string{" plan ": " pro ", "region": "", "": "x", "long": strings.Repeat("x", 1025)}
	for i := range 9 {
		attributes["k"+strconv.Itoa(i)] = "v"
	}

	consumer := mergeConsumer(nil, root.Consumer{Identifier: " acme ", Name: " Acme Corp ", Attributes: attributes})
	consumer = mergeConsumer(consumer, root.Consumer{Identifier: "acme", Group: "enterprise", Attributes: map[string]string{"plan": "free", "zone": "1"}})

	assert.Equal(t, "acme", consumer.identifier)
	assert.Equal(t, "Acme Corp", consumer.name)
	assert.Equal(t, "enterprise", consumer.group)
	assert.Equal(t, []consumerAttribute{
		{"k0", "v"}, {"k1", "v"}, {"k2", "v"}, {"k3", "v"}, {"k4", "v"}, {"k5", "v"}, {"k6", "v"}, {"k7", "v"}, {"k8", "v"},
		{"plan", "free"},
	}, consumer.attributes)
	assert.Equal(t, &requestConsumer{identifier: "globex"}, mergeConsumer(consumer, root.Consumer{Identifier: "globex"}))
}

func TestConsumerUpdateIsEmittedWhenPayloadChanges(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetConsumer(r.Context(), root.Consumer{Identifier: "acme", Name: r.URL.Query().Get("name"), Attributes: map[string]string{"plan": "pro", "region": ""}})
	}))

	for _, name := range []string{"Acme", "Acme", "Acme+Corp"} {
		testutils.Get(t, appURL+"/items?name="+name)
	}
	require.NoError(t, Shutdown(context.Background()))

	records := server.Events(t, consumerUpdateEventName)
	require.Len(t, records, 2)
	assert.Equal(t, "apitally", records[0].Scope)
	assert.Equal(t, map[string]any{
		"identifier": "acme",
		"name":       "Acme",
		"attributes": map[string]any{"plan": "pro", "region": nil},
	}, testutils.Value(records[0].Body))
	assert.Equal(t, "Acme Corp", testutils.Value(records[1].Body).(map[string]any)["name"])
}

func TestConsumerUpdateCacheEvictsLeastRecentlyUsed(t *testing.T) {
	updates := newConsumerUpdates()
	first := &requestConsumer{identifier: "first", name: "First"}
	updates.isChanged(first)
	for i := range maxCachedConsumers - 1 {
		updates.isChanged(&requestConsumer{identifier: strconv.Itoa(i), name: "x"})
	}
	assert.False(t, updates.isChanged(first))

	updates.isChanged(&requestConsumer{identifier: "new", name: "x"})
	updates.isChanged(&requestConsumer{identifier: "0", name: "x"})

	assert.True(t, updates.isChanged(&requestConsumer{identifier: "1", name: "x"}))
	assert.False(t, updates.isChanged(first))
}
