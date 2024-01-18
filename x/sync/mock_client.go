// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/ava-labs/avalanchego/x/sync (interfaces: Client)
//
// Generated by this command:
//
//	mockgen -package=sync -destination=x/sync/mock_client.go github.com/ava-labs/avalanchego/x/sync Client
//

// Package sync is a generated GoMock package.
package sync

import (
	context "context"
	reflect "reflect"

	sync "github.com/ava-labs/avalanchego/proto/pb/sync"
	merkledb "github.com/ava-labs/avalanchego/x/merkledb"
	gomock "go.uber.org/mock/gomock"
)

// MockClient is a mock of Client interface.
type MockClient struct {
	ctrl     *gomock.Controller
	recorder *MockClientMockRecorder
}

// MockClientMockRecorder is the mock recorder for MockClient.
type MockClientMockRecorder struct {
	mock *MockClient
}

// NewMockClient creates a new mock instance.
func NewMockClient(ctrl *gomock.Controller) *MockClient {
	mock := &MockClient{ctrl: ctrl}
	mock.recorder = &MockClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockClient) EXPECT() *MockClientMockRecorder {
	return m.recorder
}

// GetChangeProof mocks base method.
func (m *MockClient) GetChangeProof(arg0 context.Context, arg1 *sync.SyncGetChangeProofRequest, arg2 DB) (*merkledb.ChangeOrRangeProof, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetChangeProof", arg0, arg1, arg2)
	ret0, _ := ret[0].(*merkledb.ChangeOrRangeProof)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetChangeProof indicates an expected call of GetChangeProof.
func (mr *MockClientMockRecorder) GetChangeProof(arg0, arg1, arg2 any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetChangeProof", reflect.TypeOf((*MockClient)(nil).GetChangeProof), arg0, arg1, arg2)
}

// GetRangeProof mocks base method.
func (m *MockClient) GetRangeProof(arg0 context.Context, arg1 *sync.SyncGetRangeProofRequest) (*merkledb.RangeProof, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetRangeProof", arg0, arg1)
	ret0, _ := ret[0].(*merkledb.RangeProof)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetRangeProof indicates an expected call of GetRangeProof.
func (mr *MockClientMockRecorder) GetRangeProof(arg0, arg1 any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetRangeProof", reflect.TypeOf((*MockClient)(nil).GetRangeProof), arg0, arg1)
}
