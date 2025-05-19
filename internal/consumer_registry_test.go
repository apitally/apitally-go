package internal

import (
	"fmt"
	"testing"

	"github.com/apitally/apitally-go/common"
	"github.com/stretchr/testify/assert"
)

func TestConsumerRegistry(t *testing.T) {
	t.Run("ConsumerFromStringOrObject", func(t *testing.T) {
		// Empty string should return nil
		consumer := ConsumerFromStringOrObject("")
		assert.Nil(t, consumer)

		// Empty identifier in struct should return nil
		consumer = ConsumerFromStringOrObject(common.Consumer{Identifier: " "})
		assert.Nil(t, consumer)

		// Valid string should return consumer with identifier
		consumer = ConsumerFromStringOrObject("test")
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)

		// Valid struct should return consumer with identifier
		consumer = ConsumerFromStringOrObject(common.Consumer{Identifier: "test"})
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)

		// Consumer with name and group should trim spaces
		consumer = ConsumerFromStringOrObject(common.Consumer{
			Identifier: "test",
			Name:       "Test ",
			Group:      " Testers ",
		})
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)
		assert.NotNil(t, consumer.Name)
		assert.Equal(t, "Test", consumer.Name)
		assert.NotNil(t, consumer.Group)
		assert.Equal(t, "Testers", consumer.Group)
	})

	t.Run("AddOrUpdateConsumer", func(t *testing.T) {
		registry := NewConsumerRegistry()

		// Adding nil consumer should not panic
		registry.AddOrUpdateConsumer(nil)

		// Adding consumer without name or group should not update
		registry.AddOrUpdateConsumer(&common.Consumer{Identifier: "test"})
		assert.Empty(t, registry.GetAndResetUpdatedConsumers())

		// Adding consumer with name should update
		testConsumer := &common.Consumer{
			Identifier: "test",
			Name:       "Test",
		}
		registry.AddOrUpdateConsumer(testConsumer)
		updatedConsumers := registry.GetAndResetUpdatedConsumers()
		assert.Len(t, updatedConsumers, 1)
		assert.Equal(t, testConsumer, updatedConsumers[0])

		// Adding consumer with same name should not update
		registry.AddOrUpdateConsumer(&common.Consumer{
			Identifier: "test",
			Name:       "Test",
		})
		assert.Empty(t, registry.GetAndResetUpdatedConsumers())

		// Adding consumer with different name should update
		registry.AddOrUpdateConsumer(&common.Consumer{
			Identifier: "test",
			Name:       "Test 2",
		})
		updatedConsumers = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, updatedConsumers, 1)
		assert.Equal(t, "Test 2", updatedConsumers[0].Name)

		// Adding consumer with group should update
		registry.AddOrUpdateConsumer(&common.Consumer{
			Identifier: "test",
			Name:       "Test 2",
			Group:      "Test Group",
		})
		updatedConsumers = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, updatedConsumers, 1)
		assert.Equal(t, "Test Group", updatedConsumers[0].Group)

		// Adding consumer with same group should not update
		registry.AddOrUpdateConsumer(&common.Consumer{
			Identifier: "test",
			Name:       "Test 2",
			Group:      "Test Group",
		})
		assert.Empty(t, registry.GetAndResetUpdatedConsumers())
	})

	t.Run("GetAndResetUpdatedConsumers", func(t *testing.T) {
		registry := NewConsumerRegistry()

		// Add multiple consumers
		consumerMap := make(map[string]*common.Consumer)
		for i := 0; i < 3; i++ {
			consumer := &common.Consumer{
				Identifier: fmt.Sprintf("test%d", i),
				Name:       fmt.Sprintf("Test %d", i),
			}
			registry.AddOrUpdateConsumer(consumer)
			consumerMap[consumer.Identifier] = consumer
		}

		// Get updated consumers
		updatedConsumers := registry.GetAndResetUpdatedConsumers()
		assert.Len(t, updatedConsumers, 3)
		for _, consumer := range updatedConsumers {
			assert.Equal(t, consumerMap[consumer.Identifier], consumer)
		}

		// Get updated consumers again should return empty slice
		updatedConsumers = registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, updatedConsumers)
	})
}
