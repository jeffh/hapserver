package hapserver

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
)

type serverUpdate struct {
	Accessories []*accessory.A
}

type DynamicServer struct {
	Pin              string
	SetupId          string
	Bridge           *accessory.Bridge
	DebounceDuration time.Duration
	OnSetup          func(s *hap.Server)

	update chan serverUpdate

	fs  hap.Store
	srv *hap.Server
	as  []*accessory.A
}

func NewDynamicServer(s hap.Store, bridge *accessory.Bridge) *DynamicServer {
	return &DynamicServer{
		fs:     s,
		Bridge: bridge,
		update: make(chan serverUpdate, 8),
	}
}

// SetAccessories restarts the server with new accessories
func (s *DynamicServer) SetAccessories(as []*accessory.A) {
	c := make([]*accessory.A, len(as))
	copy(c, as)
	s.update <- serverUpdate{Accessories: c}
}

func (s *DynamicServer) SetupUri() string {
	if s.SetupId == "" {
		s.SetupId = strings.ToUpper(fmt.Sprintf("%04x", rand.Intn(0xffff)))
	}

	if s.Pin == "" {
		s.Pin = fmt.Sprintf("%08d", rand.Intn(99999999))
	}

	return SetupURI(s.Bridge.Type, s.SetupId, s.Pin)
}

func (s *DynamicServer) Run(ctx context.Context) error {
	if s.SetupId == "" {
		s.SetupId = strings.ToUpper(fmt.Sprintf("%04x", rand.Intn(0xffff)))
	}

	if s.Pin == "" {
		s.Pin = fmt.Sprintf("%08d", rand.Intn(99999999))
	}

	var stopServer context.CancelFunc
	var timer *time.Timer
	if s.DebounceDuration > 0 {
		timer = time.NewTimer(s.DebounceDuration)
		timer.Stop()
	} else {
		timer = time.NewTimer(10 * time.Second)
		timer.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			if stopServer != nil {
				stopServer()
				stopServer = nil
			}
			return nil
		case <-timer.C:
			if stopServer != nil {
				stopServer()
				stopServer = nil
			}
			slog.Info("starting from debounce", "numAccessories", len(s.as))

			var srv *hap.Server
			var err error
			ctx, cancel := context.WithCancel(ctx)
			if len(s.as) == 0 {
				srv, err = hap.NewServer(s.fs, s.Bridge.A)
			} else {
				srv, err = hap.NewServer(s.fs, s.Bridge.A, s.as...)
			}
			if err != nil {
				cancel()
				return err
			}
			srv.SetupId = s.SetupId
			srv.Pin = s.Pin
			stopServer = cancel
			if s.OnSetup != nil {
				s.OnSetup(srv)
			}
			go srv.ListenAndServe(ctx)
			timer.Stop()
		case evt := <-s.update:
			s.as = evt.Accessories
			if s.DebounceDuration > 0 && stopServer != nil {
				slog.Info("starting using debounce", "numAccessories", len(evt.Accessories))
				timer.Reset(s.DebounceDuration)
			} else {
				slog.Info("starting without debounce", "numAccessories", len(s.as))
				if stopServer != nil {
					stopServer()
					stopServer = nil
				}

				var srv *hap.Server
				var err error
				ctx, cancel := context.WithCancel(ctx)
				if len(s.as) == 0 {
					srv, err = hap.NewServer(s.fs, s.Bridge.A)
				} else {
					srv, err = hap.NewServer(s.fs, s.Bridge.A, s.as...)
				}
				if err != nil {
					cancel()
					slog.Error("failed to start server", "error", err)
					return err
				}
				srv.SetupId = s.SetupId
				srv.Pin = s.Pin
				stopServer = cancel
				slog.Info("Starting")
				if s.OnSetup != nil {
					s.OnSetup(srv)
				}
				go srv.ListenAndServe(ctx)
				timer.Stop()
			}
		}
	}
}

func SetupURI(accessoryType byte, setupId, pin string) string {
	pinNum, err := strconv.ParseUint(pin, 10, 64)
	if err != nil {
		pinNum = 0
	}
	const reserved = 0
	const flags = 2 // 2=IP, 4=BLE, 8=IP_WAC
	const version = 0
	value := 0 | uint64(version&0x7)

	value <<= 4
	value |= reserved & 0xf

	value <<= 8
	value |= uint64(accessoryType) & 0xff

	value <<= 4
	value |= flags & 0xf

	value <<= 27
	value |= uint64(pinNum) & 0x7fffffff

	padded := strings.ToUpper(strconv.FormatUint(value, 36))
	if len(padded) != 9 {
		pad := make([]byte, 9-len(padded))
		for i := range pad {
			pad[i] = '0'
		}
		padded = string(pad) + padded
	}

	return "X-HM://" + padded + setupId
}
