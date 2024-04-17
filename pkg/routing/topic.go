package routing

import (
	"encoding/json"

	"github.com/rs/zerolog/log"
	msg "github.com/nusa-exchange/rango/pkg/message"
)

type Topic struct {
	hub     *Hub
	clients map[IClient]struct{}
}

func NewTopic(h *Hub) *Topic {
	return &Topic{
		clients: make(map[IClient]struct{}),
		hub:     h,
	}
}

func eventMust(method string, data interface{}) []byte {
	ev, err := msg.PackOutgoingEvent(method, data)
	if err != nil {
		log.Panic().Msg(err.Error())
	}

	return ev
}

func contains(list []string, el string) bool {
	for _, l := range list {
		if l == el {
			return true
		}
	}
	return false
}

func (t *Topic) len() int {
	return len(t.clients)
}

func (t *Topic) broadcast(message *Event) {
	var bodyMsg interface{}

	if err := json.Unmarshal(message.Body, &bodyMsg); err != nil {
		log.Error().Msgf("Fail to JSON marshal: %s", err.Error())
		return
	}

	body, err := json.Marshal(map[string]interface{}{
		message.Topic: bodyMsg,
	})

	if err != nil {
		log.Error().Msgf("Fail to JSON marshal: %s", err.Error())
		return
	}

	for client := range t.clients {
		client.Send(string(body))
	}
}

func (t *Topic) broadcastRaw(msg []byte) {
	for client := range t.clients {
		client.Send(string(msg))
	}
}

func (t *Topic) subscribe(c IClient) bool {
	if _, ok := t.clients[c]; ok {
		return false
	}
	t.clients[c] = struct{}{}

	return true
}

func (t *Topic) unsubscribe(c IClient) bool {
	_, ok := t.clients[c]
	delete(t.clients, c)

	return ok
}
