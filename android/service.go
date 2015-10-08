package android

import (
	"log"

	"github.com/omerkirk/gcm"
)

const (
	// Maximum number of messages to be queued
	maxNumberOfMessages = 100000

	// Response channel buffer size
	responseChannelBufferSize = 1000
)

type Service struct {
	gcmClient   *gcm.Sender
	senderCount int
	retryCount  int

	isProduction bool

	respCh   chan *ServiceResponse
	msgQueue chan *gcm.Message
}

func StartService(apiKey string, senderCount, retryCount int, isProduction bool) *Service {
	gcmService := &Service{
		gcmClient: &gcm.Sender{ApiKey: apiKey},

		senderCount: senderCount,
		retryCount:  retryCount,

		isProduction: isProduction,

		respCh: make(chan *ServiceResponse, responseChannelBufferSize),

		msgQueue: make(chan *gcm.Message, maxNumberOfMessages)}

	for i := 0; i < senderCount; i++ {
		go gcmService.sender()
	}
	return gcmService
}

func (s *Service) Queue(msg *gcm.Message) {
	if s.isProduction {
		msg.DryRun = false
	} else {
		msg.DryRun = true
	}
	s.msgQueue <- msg
}

func (s *Service) Listen() chan *ServiceResponse {
	return s.respCh
}

func (s *Service) sender() {
	for {
		select {
		case msg := <-s.msgQueue:
			resp, err := s.gcmClient.Send(msg, s.retryCount)
			if err != nil {
				log.Println("pushgo error: ", err)
			} else {
				s.respCh <- NewServiceResponse(resp, msg)
			}

		}
	}
}
