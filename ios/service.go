package ios

import (
	"crypto/tls"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/RobotsAndPencils/buford/certificate"
	"github.com/RobotsAndPencils/buford/payload"
	"github.com/RobotsAndPencils/buford/push"
	"github.com/omerkirk/pushgo/core"
)

const (
	// Maximum number of messages to be queued
	maxNumberOfMessages = 100000000

	// Response channel buffer size
	responseChannelBufferSize = 100000
)

type Service struct {
	certificate tls.Certificate
	bundleID    string

	senderCount int

	isProduction bool

	respCh chan *core.Response

	msgQueue chan *message
}

func New(certName, passwd string, bundleID string, senderCount int, isProduction bool) *Service {
	cert, err := certificate.Load(certName, passwd)
	if err != nil {
		log.Fatal(err)
	}
	s := &Service{
		certificate:  cert,
		bundleID:     bundleID,
		isProduction: isProduction,

		senderCount: senderCount,

		respCh: make(chan *core.Response, responseChannelBufferSize),

		msgQueue: make(chan *message, maxNumberOfMessages)}

	for i := 0; i < senderCount; i++ {
		go s.sender()
	}
	return s
}

func (s *Service) Queue(msg *core.Message) {
	p := payload.APS{
		Alert: payload.Alert{Body: msg.Alert},
		Sound: msg.Sound}

	pm := p.Map()
	for k, v := range msg.Custom {
		pm[k] = v
	}
	b, err := json.Marshal(pm)
	if err != nil {
		log.Printf("pushgo: ios queue error: cannot convert msg to json %v\n", pm)
		return
	}
	msg.Bytes = b

	go s.msgDistributor(msg)
}

func (s *Service) Listen() chan *core.Response {
	return s.respCh
}

func (s *Service) msgDistributor(msg *core.Message) {
	respCh := make(chan push.Response, responseChannelBufferSize)
	sr := &core.Response{
		Extra:     msg.Extra,
		ReasonMap: make(map[error]int),
	}
	h := &push.Headers{
		Topic:      s.bundleID,
		Expiration: time.Now().Add(time.Second * time.Duration(msg.Expiration))}
	if msg.Priority == core.PriorityNormal {
		h.LowPriority = true
	}
	groupSize := (len(msg.Devices) / s.senderCount) + 1
	deviceGroups := core.DeviceList(msg.Devices).Group(groupSize)
	for i := 0; i < len(deviceGroups); i++ {
		s.msgQueue <- &message{payload: msg.Bytes, devices: deviceGroups[i], headers: h, respCh: respCh}
	}

	for {
		select {
		case iosResp := <-respCh:
			sr.Total++
			if iosResp.Err != nil {
				var err error
				e, ok := iosResp.Err.(*push.Error)
				if !ok {
					err = iosResp.Err
				} else {
					err = e.Reason
				}
				sr.Failure++
				if count, ok := sr.ReasonMap[err]; ok {
					sr.ReasonMap[err] = count + 1
				} else {
					sr.ReasonMap[err] = 1
				}
				// IOs specific error can be returned even if the returned error is not of type push.Error
				if e != nil && (err == push.ErrUnregistered || err == push.ErrDeviceTokenNotForTopic) {
					sp := core.Result{}
					sp.Type = core.ResponseTypeDeviceExpired
					sp.RegistrationID = iosResp.DeviceToken
					sr.Results = append(sr.Results, sp)
				}
			} else {
				sr.Success++
			}
			if sr.Total == len(msg.Devices) {
				s.respCh <- sr
				return
			}
		}
	}

}

type message struct {
	payload []byte
	devices []string
	headers *push.Headers

	respCh chan push.Response
}

func (s *Service) sender() {
	client, err := push.NewClient(s.certificate)
	if err != nil {
		log.Fatal(err)
	}
	apns := &push.Service{
		Client: client}
	if s.isProduction {
		apns.Host = push.Production
	} else {
		apns.Host = push.Development
	}

	for {
		select {
		case mr := <-s.msgQueue:
			queue := push.NewQueue(apns, 250)
			var wg sync.WaitGroup
			go func() {
				for r := range queue.Responses {
					resp := r
					mr.respCh <- resp
					wg.Done()
				}
			}()

			for i := 0; i < len(mr.devices); i++ {
				wg.Add(1)
				queue.Push(mr.devices[i], mr.headers, mr.payload)
			}
			wg.Wait()
			queue.Close()
		}
	}
}
