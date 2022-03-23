/*
Copyright 2021 The KubeEdge Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package messagelayer

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"

	wsContext "metaedge/pkg/messagelayer/ws"
)

// MessageLayer define all functions that message layer must implement
type MessageLayer interface {
	SendResourceObject(nodeName string, eventType watch.EventType, obj interface{}) error
	ReceiveResourceUpdate() (*ResourceUpdateSpec, error)
	Done() <-chan struct{}
}

// ContextMessageLayer build on context
type ContextMessageLayer struct {
}

// ResourceUpdateSpec describes the resource update from upstream
type ResourceUpdateSpec struct {
	Kind      string
	Namespace string
	Name      string
	Operation string
	Content   []byte
}

// SendResourceObject message to the node with resource object and event type
func (cml *ContextMessageLayer) SendResourceObject(nodeName string, eventType watch.EventType, obj interface{}) error {
	var operation string
	switch eventType {
	case watch.Added:
		operation = "insert"
	case watch.Modified:
		operation = "update"
	case watch.Deleted:
		operation = "delete"
	default:
		// should never get here
		return fmt.Errorf("event type: %s unsupported", eventType)
	}

	var msg Message
	payload, _ := json.Marshal(obj)
	msg.Content = payload

	// For code simplicity not to duplicate the code,
	// here just unmarshal to unifying struct type.
	var om metav1.PartialObjectMetadata
	err := json.Unmarshal(payload, &om)
	if err != nil {
		// impossible here, just for in case
		return fmt.Errorf("unmarshal error for %v, err: %w", obj, err)
	}

	namespace := om.Namespace
	kind := strings.ToLower(om.Kind)
	name := om.Name

	msg.Namespace = namespace
	msg.ResourceKind = kind
	msg.ResourceName = name
	msg.Operation = operation

	klog.V(2).Infof("sending %s %s/%s to node(%s)", kind, namespace, name, nodeName)
	klog.V(4).Infof("sending %s %s/%s to node(%s), msg:%s", kind, namespace, name, nodeName, msg)
	// TODO: may need to guarantee message send to node
	return wsContext.SendToEdge(nodeName, &msg)
}

// ReceiveResourceUpdate receives and handles the update
func (cml *ContextMessageLayer) ReceiveResourceUpdate() (*ResourceUpdateSpec, error) {
	nodeName, msg, err := wsContext.ReceiveFromEdge()
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("get message from nodeName %s:%s", nodeName, msg)
	namespace := msg.Namespace
	kind := strings.ToLower(msg.ResourceKind)
	name := msg.ResourceName
	operation := msg.Operation
	content := msg.Content

	return &ResourceUpdateSpec{
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		Operation: operation,
		Content:   content,
	}, nil
}

// Done signals the message layer is done
func (cml *ContextMessageLayer) Done() <-chan struct{} {
	return wsContext.Done()
}

// NewContextMessageLayer create a ContextMessageLayer
func NewContextMessageLayer() MessageLayer {
	return &ContextMessageLayer{}
}
