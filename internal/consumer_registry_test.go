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
		consumer = ConsumerFromStringOrObject(&common.ApitallyConsumer{Identifier: " "})
		assert.Nil(t, consumer)

		// Valid string should return consumer with identifier
		consumer = ConsumerFromStringOrObject("test")
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)

		// Valid struct should return consumer with identifier
		consumer = ConsumerFromStringOrObject(&common.ApitallyConsumer{Identifier: "test"})
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)

		// Consumer with name and group should trim spaces
		name := "Test "
		group := " Testers "
		consumer = ConsumerFromStringOrObject(&common.ApitallyConsumer{
			Identifier: "test",
			Name:       &name,
			Group:      &group,
		})
		assert.NotNil(t, consumer)
		assert.Equal(t, "test", consumer.Identifier)
		assert.NotNil(t, consumer.Name)
		assert.Equal(t, "Test", *consumer.Name)
		assert.NotNil(t, consumer.Group)
		assert.Equal(t, "Testers", *consumer.Group)
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
		name := "Test"
		group := "Testers"
		testConsumer := &common.ApitallyConsumer{
			Identifier: "test",
			Name:       &name,
			Group:      &group,
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
		newName := "Test 2"
		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test",
			Name:       &newName,
			Group:      &group,
		})
		data = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, data, 1)
		assert.Equal(t, "Test 2", *data[0].Name)

		// Changing group should mark as updated
		newGroup := "Testers 2"
		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test",
			Name:       &newName,
			Group:      &newGroup,
		})
		data = registry.GetAndResetUpdatedConsumers()
		assert.Len(t, data, 1)
		assert.Equal(t, "Testers 2", *data[0].Group)
	})

	t.Run("GetAndResetUpdatedConsumers", func(t *testing.T) {
		registry := NewConsumerRegistry()

		// Empty registry should return empty slice
		data := registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, data)

		// Add multiple consumers
		name1, group1 := "Test 1", "Group 1"
		name2, group2 := "Test 2", "Group 2"

		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test1",
			Name:       &name1,
			Group:      &group1,
		})

		registry.AddOrUpdateConsumer(&common.ApitallyConsumer{
			Identifier: "test2",
			Name:       &name2,
			Group:      &group2,
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
		assert.Equal(t, "Test 1", *consumerMap["test1"].Name)
		assert.Equal(t, "Test 2", *consumerMap["test2"].Name)

		// After reset, should return empty slice
		data = registry.GetAndResetUpdatedConsumers()
		assert.Empty(t, data)
	})
}
