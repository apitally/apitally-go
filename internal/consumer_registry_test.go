package internal

import (
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
		consumer = ConsumerFromStringOrObject(common.ApitallyConsumer{Identifier: " "})
		assert.Nil(t, consumer)

		// Valid string should return consumer with identifier
		consumer = ConsumerFromStringOrObject("test")
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)

		// Valid struct should return consumer with identifier
		consumer = ConsumerFromStringOrObject(common.ApitallyConsumer{Identifier: "test"})
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)

		// Consumer with name and group should trim spaces
		consumer = ConsumerFromStringOrObject(common.ApitallyConsumer{
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
		// Create a new registry
		registry := NewConsumerRegistry()

		// Adding nil consumer should not change anything
		registry.AddOrUpdateConsumer(nil)
		data := registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, data)

		// Adding consumer without name or group should not change anything
		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{Identifier: "test"})
		data = registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, data)

		// Adding consumer with name should work
		testConsumer := &common.ApitallyConsumer{
			Identifier: "test",
			Name:       "Test",
			Group:      "Testers",
		}
		registry.AddOrUpdateConsumer(testConsumer)
		data = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, data, 1)
		assert.Equal(t, testConsumer, data[0])

		// Adding same consumer again should not mark as updated
		registry.AddOrUpdateConsumer(testConsumer)
		data = registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, data)

		// Changing name should mark as updated
		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test",
			Name:       "Test 2",
			Group:      "Testers",
		})
		data = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, data, 1)
		assert.Equal(t, "Test 2", data[0].Name)

		// Changing group should mark as updated
		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test",
			Name:       "Test 2",
			Group:      "Testers 2",
		})
		data = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, data, 1)
		assert.Equal(t, "Testers 2", data[0].Group)
	})

	t.Run("GetAndResetUpdatedConsumers", func(t *testing.T) {
		registry := NewConsumerRegistry()

		// Empty registry should return empty slice
		data := registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, data)

		// Add multiple consumers
		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test1",
			Name:       "Test 1",
			Group:      "Group 1",
		})

		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test2",
			Name:       "Test 2",
			Group:      "Group 2",
		})

		// Should get both consumers
		data = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, data, 2)

		// Map to check both consumers exist
		consumerMap := make(map[string]*common.ApitallyConsumer)
		for _, c := range data {
			consumerMap[c.Identifier] = c
		}

		assert.Contains(t, consumerMap, "test1")
		assert.Contains(t, consumerMap, "test2")
		assert.Equal(t, "Test 1", consumerMap["test1"].Name)
		assert.Equal(t, "Test 2", consumerMap["test2"].Name)

		// After reset, should return empty slice
		data = registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, data)
	})
}
