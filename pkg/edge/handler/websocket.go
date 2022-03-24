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

package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"

	"metaedge/cmd/metaedge-edge/app/options"
	"metaedge/pkg/edge/common"
	"metaedge/pkg/messagelayer"
)

// wsClient defines a websocket client
type wsClient struct {
	Options             *options.LocalControllerOptions
	WSConnection        *WSConnection
	SubscribeMessageMap map[string]MessageResourceHandler
	SendMessageChannel  chan messagelayer.Message
	ReconnectChannel    chan struct{}
}

// WSConnection defines conn
type WSConnection struct {
	WSConn *websocket.Conn
}

const (
	// RetryCount is count of retrying to connecting to cloud
	RetryCount = 5
	// RetryConnectIntervalSeconds is interval time of retrying to connecting to cloud
	RetryConnectIntervalSeconds = 5
	// MessageChannelCacheSize is size of channel cache
	MessageChannelCacheSize = 100
)

// NewWebSocketClient creates client
func NewWebSocketClient(options *options.LocalControllerOptions) ClientI {
	c := wsClient{
		Options:             options,
		SubscribeMessageMap: make(map[string]MessageResourceHandler),
		SendMessageChannel:  make(chan messagelayer.Message, MessageChannelCacheSize),
	}

	return &c
}

// Subscribe registers in client
func (c *wsClient) Subscribe(m MessageResourceHandler) error {
	name := m.GetName()
	if c.SubscribeMessageMap[name] == nil {
		c.SubscribeMessageMap[name] = m
	} else {
		klog.Warningf("%s had been registered in websocket client", name)
	}

	return nil
}

// handleReceivedMessage handles received message
func (c *wsClient) handleReceivedMessage(stop chan struct{}) {
	defer func() {
		stop <- struct{}{}
	}()

	ws := c.WSConnection.WSConn

	for {
		message := messagelayer.Message{}
		if err := ws.ReadJSON(&message); err != nil {
			klog.Errorf("client received message from cloud(address: %s) failed, error: %v",
				c.Options.CloudAddr, err)
			return
		}

		klog.V(2).Infof("client received message header: %+v from cloud(address: %s)",
			message.MessageHeader, c.Options.CloudAddr)
		klog.V(4).Infof("client received message content: %s from cloud(address: %s)",
			message.Content, c.Options.CloudAddr)

		m := c.SubscribeMessageMap[message.ResourceKind]
		if m != nil {
			go func() {
				var err error
				switch message.Operation {
				case InsertOperation:
					err = m.Insert(&message)

				case DeleteOperation:
					err = m.Delete(&message)
				default:
					err = fmt.Errorf("unknown operation: %s", message.Operation)
				}
				if err != nil {
					klog.Errorf("failed to handle message(%+v): %v", message.MessageHeader, err)
				}
			}()
		} else {
			klog.Errorf("%s hadn't registered in websocket client", message.ResourceKind)
		}
	}
}

// WriteMessage saves message in a queue
func (c *wsClient) WriteMessage(messageBody interface{}, messageHeader messagelayer.MessageHeader) error {
	content, err := json.Marshal(&messageBody)
	if err != nil {
		return err
	}

	message := messagelayer.Message{
		MessageHeader: messageHeader,
		Content:       content,
	}

	c.SendMessageChannel <- message

	return nil
}

// sendMessage sends the message through the connection
func (c *wsClient) sendMessage(stop chan struct{}) {
	defer func() {
		stop <- struct{}{}
	}()

	ws := c.WSConnection.WSConn

	for {
		message, ok := <-c.SendMessageChannel
		if !ok {
			return
		}

		if err := ws.WriteJSON(&message); err != nil {
			klog.Errorf("client sent message to cloud(address: %s) failed, error: %v",
				c.Options.CloudAddr, err)

			c.SendMessageChannel <- message

			return
		}

		klog.V(2).Infof("client sent message header: %+v to cloud(address: %s)",
			message.MessageHeader, c.Options.CloudAddr)
		klog.V(4).Infof("client sent message content: %s to cloud(address: %s)",
			message.Content, c.Options.CloudAddr)
	}
}

// connect tries to connect remote server
func (c *wsClient) connect() error {
	header := http.Header{}
	header.Add(common.WSHeaderNodeName, c.Options.NodeName)
	u := url.URL{Scheme: common.WSScheme, Host: c.Options.CloudAddr, Path: "/"}

	klog.Infof("client starts to connect cloud(address: %s)", c.Options.CloudAddr)

	for i := 0; i < RetryCount; i++ {
		wsConn, _, err := websocket.DefaultDialer.Dial(u.String(), header)

		if err == nil {
			if errW := wsConn.WriteJSON(&messagelayer.MessageHeader{}); errW != nil {
				return errW
			}

			c.WSConnection = &WSConnection{WSConn: wsConn}
			klog.Infof("websocket connects cloud(address: %s) successful", c.Options.CloudAddr)

			return nil
		}

		klog.Errorf("client tries to connect cloud(address: %s) failed, error: %v",
			c.Options.CloudAddr, err)

		time.Sleep(time.Duration(RetryConnectIntervalSeconds) * time.Second)
	}

	errorMsg := fmt.Errorf("max retry count reached when connecting cloud(address: %s)",
		c.Options.CloudAddr)
	klog.Errorf("%v", errorMsg)

	return errorMsg
}

// Start starts websocket client
func (c *wsClient) Start() error {
	go c.reconnect()

	return nil
}

// reconnect reconnects cloud
func (c *wsClient) reconnect() {
	for {
		if err := c.connect(); err != nil {
			continue
		}
		ws := c.WSConnection.WSConn

		stop := make(chan struct{}, 2)
		go c.handleReceivedMessage(stop)
		go c.sendMessage(stop)
		<-stop

		_ = ws.Close()
	}
}
