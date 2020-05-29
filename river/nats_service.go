package river

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"gopkg.in/birkirb/loggers.v1"
)

var sync2Nats = make(chan *Message, 4096)

type Message struct {
	// Id is an unique identifier of action.
	Id uint64

	// Type id of the event
	Type uint32

	// event name: users/posts/comments etc
	Name string

	// action usually insert/update/delete
	Action string

	// Metadata contains the message metadata.
	//
	// Can be used to store data which doesn't require unmarshaling entire payload.
	// It is something similar to HTTP request's headers.
	Metadata map[string]string

	// Payload is message's payload.
	Payload []interface{}

	CreatedAt time.Time
}

// NatsService represents a service
type NatsService struct {
	ctx           context.Context
	cancel        context.CancelFunc
	log           loggers.Advanced
	riverInstance *River
	sphm          sync.Mutex
	wg            sync.WaitGroup

	seq uint64

	// NATS connection types
	nc *nats.Conn
	ec *nats.EncodedConn

	// Send Channel
	RequestChanSend chan *Message
}

// Serve suture.Service implementation
func (s *NatsService) Serve() {
	s.log.Info("Serve() started")
	s.wg.Add(1)
	s.connect()
	defer func() {
		s.wg.Done()
		s.disconnect()
		s.log.Info("Serve() exited")
	}()

	s.ctx, s.cancel = context.WithCancel(s.riverInstance.ctx)
	s.SyncLoop(s.ctx)
}

// Stop suture.Service implementation
func (s *NatsService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *NatsService) String() string {
	return "NatsService"
}

func (s *NatsService) SyncLoop(ctx context.Context) {

	for {
		if s.ec == nil {
			s.log.Debug("SyncLoop Nats not connected, so drain channel")
			<-sync2Nats
			continue
		}

		select {
		case v := <-sync2Nats:
			s.Publish(v)
		case <-s.ctx.Done():
			s.log.Info("SyncLoop Closed")
			break
		default:
			s.log.Debug("SyncLoop No activity")
		}
	}

	close(sync2Nats)
	s.log.Info("SyncLoop exited")
}

func (s *NatsService) Publish(v *Message) {
	if s.ec == nil {
		return
	}

	cfg := s.riverInstance.c
	typ := strconv.FormatUint(uint64(v.Type), 10)

	// try to get name from tao map
	if name, ok := cfg.TaoMap[typ]; ok {
		v.Name = name
	}

	sub := fmt.Sprintf("binlog.%s.%s.%d", v.Name, v.Action, s.nextSeqNo())
	s.log.Infof("Publishing event [%s] for [%d]", sub, v.Id)

	if err := s.ec.Publish(sub, v); err != nil {
		s.log.Errorf("Error publishing event [%s] for [%d]: %v", sub, v.Id, err)
	}

	// // Sends a PING and wait for a PONG from the server, up to the given timeout.
	// // This gives guarantee that the server has processed the above message.
	// if err := s.ec.FlushTimeout(time.Second); err != nil {
	// 	s.log.Errorf("Flush Error %d: %v", v.Id, err)
	// }
}

// NewNatsService service constructor
func NewNatsService(r *River) *NatsService {
	s := NatsService{riverInstance: r}
	s.log = r.Log.WithFields("service", s.String())
	return &s
}

func (s *NatsService) nextSeqNo() uint64 {
	s.seq++

	return s.seq
}

func (s *NatsService) connect() {
	s.sphm.Lock()
	defer s.sphm.Unlock()
	var err error

	if !s.riverInstance.c.NatsEnabled {
		return
	}

	// Connect Options.
	opts := []nats.Option{nats.Name("NATS MySQL Manticore Publisher")}
	opts = setupConnOptions(opts, s.log)

	// Connect to NATS
	s.nc, err = nats.Connect(s.riverInstance.c.NatsAddr, opts...)
	if err != nil {
		s.log.Errorf("Error connecting nats: %v", err)
		return
	}

	s.ec, err = nats.NewEncodedConn(s.nc, nats.GOB_ENCODER)
	if err != nil {
		s.log.Errorf("Error encoding connection nats: %v", err)
		return
	}

	// Bind send channel
	s.RequestChanSend = make(chan *Message)
	s.ec.BindSendChan("binlog", s.RequestChanSend)

	s.log.Infof("Connected [%s]", s.nc.ConnectedUrl())
}

func (s *NatsService) disconnect() {
	s.sphm.Lock()
	defer s.sphm.Unlock()

	if s.ec != nil {
		s.ec.Close()
	}
}

func setupConnOptions(opts []nats.Option, log loggers.Advanced) []nats.Option {
	totalWait := 10 * time.Minute
	reconnectDelay := time.Second

	opts = append(opts, nats.ReconnectWait(reconnectDelay))
	opts = append(opts, nats.MaxReconnects(int(totalWait/reconnectDelay)))
	opts = append(opts, nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
		log.Warnf("Disconnected due to:%s, will attempt reconnects for %.0fm", err, totalWait.Minutes())
	}))
	opts = append(opts, nats.ReconnectHandler(func(nc *nats.Conn) {
		log.Warnf("Reconnected [%s]", nc.ConnectedUrl())
	}))
	opts = append(opts, nats.ClosedHandler(func(nc *nats.Conn) {
		log.Errorf("Exiting: %v", nc.LastError())
	}))

	return opts
}

func NewMessage(id uint64) *Message {
	return &Message{
		Id:        id,
		Metadata:  make(map[string]string),
		CreatedAt: time.Now().UTC(),
	}
}

func PublishRowToNats(id uint64, typ uint32, action string, table string) {
	ev := NewMessage(id)
	ev.Type = typ
	ev.Name = table
	ev.Action = action
	ev.Metadata["tableName"] = table

	sync2Nats <- ev
}

func PublishDocToNats(doc TableRowChange, rule IngestRule) {
	ev := NewMessage(doc.DocID)
	ev.Type = uint32(rule.JsonTypeValue)
	ev.Name = doc.TableName
	ev.Action = doc.Action

	ev.Metadata["index"] = doc.Index
	ev.Metadata["tableName"] = doc.TableName
	ev.Metadata["timeStamp"] = doc.TS.String()

	// ev.Payload = doc.NewRow
	// if doc.Action == "delete" {
	// 	ev.Payload = doc.OldRow
	// }

	sync2Nats <- ev
}
