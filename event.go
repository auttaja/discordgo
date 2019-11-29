package discordgo

import (
	"encoding/json"

	nats "github.com/nats-io/nats.go"
)

// EventHandler is an interface for Discord events.
type EventHandler interface {
	// Type returns the type of event this handler belongs to.
	Type() string

	// Handle is called whenever an event of Type() happens.
	// It is the receivers responsibility to type assert that the interface
	// is the expected struct.
	Handle(*Session, interface{})
}

// EventInterfaceProvider is an interface for providing empty interfaces for
// Discord events.
type EventInterfaceProvider interface {
	// Type is the type of event this handler belongs to.
	Type() string

	// New returns a new instance of the struct this event handler handles.
	// This is called once per event.
	// The struct is provided to all handlers of the same Type().
	New() interface{}
}

var natsSubscriptions map[string]*nats.Subscription = make(map[string]*nats.Subscription)

// interfaceEventType is the event handler type for interface{} events.
const interfaceEventType = "__INTERFACE__"

// interfaceEventHandler is an event handler for interface{} events.
type interfaceEventHandler func(*Session, interface{})

// Type returns the event type for interface{} events.
func (eh interfaceEventHandler) Type() string {
	return interfaceEventType
}

// Handle is the handler for an interface{} event.
func (eh interfaceEventHandler) Handle(s *Session, i interface{}) {
	eh(s, i)
}

var registeredInterfaceProviders = map[string]EventInterfaceProvider{}

// registerInterfaceProvider registers a provider so that DiscordGo can
// access it's New() method.
func registerInterfaceProvider(eh EventInterfaceProvider) {
	if _, ok := registeredInterfaceProviders[eh.Type()]; ok {
		return
		// XXX:
		// if we should error here, we need to do something with it.
		// fmt.Errorf("event %s already registered", eh.Type())
	}
	registeredInterfaceProviders[eh.Type()] = eh
	return
}

// eventHandlerInstance is a wrapper around an event handler, as functions
// cannot be compared directly.
type eventHandlerInstance struct {
	eventHandler EventHandler
}

// addEventHandler adds an event handler that will be fired anytime
// the Discord WSAPI matching eventHandler.Type() fires.
func (s *Session) addEventHandler(eventHandler EventHandler) func() {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()

	if s.handlers == nil {
		s.handlers = map[string][]*eventHandlerInstance{}
	}

	ehi := &eventHandlerInstance{eventHandler}
	s.handlers[eventHandler.Type()] = append(s.handlers[eventHandler.Type()], ehi)

	return func() {
		s.removeEventHandlerInstance(eventHandler.Type(), ehi)
	}
}

// addEventHandler adds an event handler that will be fired the next time
// the Discord WSAPI matching eventHandler.Type() fires.
func (s *Session) addEventHandlerOnce(eventHandler EventHandler) func() {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()

	if s.onceHandlers == nil {
		s.onceHandlers = map[string][]*eventHandlerInstance{}
	}

	ehi := &eventHandlerInstance{eventHandler}
	s.onceHandlers[eventHandler.Type()] = append(s.onceHandlers[eventHandler.Type()], ehi)

	return func() {
		s.removeEventHandlerInstance(eventHandler.Type(), ehi)
	}
}

// AddHandler allows you to add an event handler that will be fired anytime
// the Discord WSAPI event that matches the function fires.
// The first parameter is a *Session, and the second parameter is a pointer
// to a struct corresponding to the event for which you want to listen.
//
// eg:
//     Session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
//     })
//
// or:
//     Session.AddHandler(func(s *discordgo.Session, m *discordgo.PresenceUpdate) {
//     })
//
// List of events can be found at this page, with corresponding names in the
// library for each event: https://discordapp.com/developers/docs/topics/gateway#event-names
// There are also synthetic events fired by the library internally which are
// available for handling, like Connect, Disconnect, and RateLimit.
// events.go contains all of the Discord WSAPI and synthetic events that can be handled.
//
// The return value of this method is a function, that when called will remove the
// event handler.
func (s *Session) AddHandler(handler interface{}) func() {
	eh := handlerForInterface(handler)

	if eh == nil {
		s.log(LogError, "Invalid handler type, handler will never be called")
		return func() {}
	}

	if s.NATS != nil && s.NatsMode == 1 {
		subject := eh.Type()
		if subject == interfaceEventType {
			subject = "*"
		}
		if _, ok := natsSubscriptions[subject]; !ok {
			s.log(LogInformational, "Subscribing to NATS event: %s", subject)
			sub, err := s.NATS.QueueSubscribe(subject, s.NatsQueueName, s.natsHandler)
			if err != nil {
				s.log(LogError, "Could not subscribe to NATS event: %s", err)
				natsSubscriptions[subject] = sub
			}
			s.log(LogInformational, "%v", natsSubscriptions)
		}
	}

	return s.addEventHandler(eh)
}

// AddHandlerOnce allows you to add an event handler that will be fired the next time
// the Discord WSAPI event that matches the function fires.
// See AddHandler for more details.
func (s *Session) AddHandlerOnce(handler interface{}) func() {
	eh := handlerForInterface(handler)

	if eh == nil {
		s.log(LogError, "Invalid handler type, handler will never be called")
		return func() {}
	}

	return s.addEventHandlerOnce(eh)
}

// removeEventHandler instance removes an event handler instance.
func (s *Session) removeEventHandlerInstance(t string, ehi *eventHandlerInstance) {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()

	handlers := s.handlers[t]
	for i := range handlers {
		if handlers[i] == ehi {
			s.handlers[t] = append(handlers[:i], handlers[i+1:]...)
		}
	}

	onceHandlers := s.onceHandlers[t]
	for i := range onceHandlers {
		if onceHandlers[i] == ehi {
			s.onceHandlers[t] = append(onceHandlers[:i], handlers[i+1:]...)
		}
	}
}

// Handles calling permanent and once handlers for an event type.
func (s *Session) handle(t string, i interface{}) {
	for _, eh := range s.handlers[t] {
		if s.SyncEvents {
			eh.eventHandler.Handle(s, i)
		} else {
			go eh.eventHandler.Handle(s, i)
		}
	}

	if len(s.onceHandlers[t]) > 0 {
		for _, eh := range s.onceHandlers[t] {
			if s.SyncEvents {
				eh.eventHandler.Handle(s, i)
			} else {
				go eh.eventHandler.Handle(s, i)
			}
		}
		s.onceHandlers[t] = nil
	}
}

// Handles events coming in from NATS
func (s *Session) natsHandler(m *nats.Msg) {
	if eh, ok := registeredInterfaceProviders[m.Subject]; ok {
		i := eh.New()
		// Attempt to unmarshal our event.
		if err := json.Unmarshal(m.Data, i); err != nil {
			s.log(LogError, "error unmarshalling %s event, %s", m.Subject, err)
		}
		s.handleEvent(m.Subject, i)
	}
}

// Handles an event type by calling internal methods, firing handlers and firing the
// interface{} event.
func (s *Session) handleEvent(t string, i interface{}) {
	s.handlersMu.RLock()
	defer s.handlersMu.RUnlock()

	if s.State != nil {
		// All events are dispatched internally first.
		s.onInterface(i)
	}

	// Then they are dispatched to anyone handling interface{} events.
	s.handle(interfaceEventType, i)

	// Finally they are dispatched to any typed handlers.
	s.handle(t, i)
}

// setGuildIds will set the GuildID on all the members of a guild.
// This is done as event data does not have it set.
func setGuildIds(g *Guild) {
	for _, c := range g.Channels {
		c.GuildID = g.ID
	}

	for _, m := range g.Members {
		m.GuildID = g.ID
	}

	for _, vs := range g.VoiceStates {
		vs.GuildID = g.ID
	}
}

func (s *Session) setSession(g *Guild) {
	g.Session = s
	for _, c := range g.Channels {
		c.Session = s
	}

	for _, m := range g.Members {
		m.User.Session = s
		m.GuildID = g.ID
	}

	for _, r := range g.Roles {
		r.Session = s
		r.Guild = g
	}

	for _, e := range g.Emojis {
		e.Session = s
		e.Guild = g
	}
}

// onInterface handles all internal events and routes them to the appropriate internal handler.
func (s *Session) onInterface(i interface{}) {
	switch t := i.(type) {
	case *Ready:
		for _, g := range t.Guilds {
			setGuildIds(g)
			s.setSession(g)
		}
		s.onReady(t)
	case *GuildCreate:
		setGuildIds(t.Guild)
	case *GuildUpdate:
		setGuildIds(t.Guild)
	case *VoiceServerUpdate:
		go s.onVoiceServerUpdate(t)
	case *VoiceStateUpdate:
		go s.onVoiceStateUpdate(t)
	}

	if s.State == nil {
		panic("the state is nil in onInterface")
	}

	err := s.State.OnInterface(s, i)
	if err != nil {
		s.log(LogDebug, "error dispatching internal event, %s", err)
	}
}

// onReady handles the ready event.
func (s *Session) onReady(r *Ready) {

	// Store the SessionID within the Session struct.
	s.sessionID = r.SessionID
}
