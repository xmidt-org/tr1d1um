package hooks

import (
	"context"
	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/argus/model"
)

type MockHookPusherStore struct {
	mock.Mock
}

func (m *MockHookPusherStore) Stop(context context.Context) {
	panic("implement me")
}

func (m *MockHookPusherStore) Push(item model.Item, owner string) (string, error) {
	args := m.Called(item, owner)
	return args.Get(0).(string), args.Error(1)
}

func (m *MockHookPusherStore) GetItems(owner string) ([]model.Item, error) {
	args := m.Called(owner)
	return args.Get(0).([]model.Item), args.Error(1)

}

func (m *MockHookPusherStore) Remove(id string, owner string) (model.Item, error) {
	args := m.Called(id, owner)
	return args.Get(0).(model.Item), args.Error(1)
}
