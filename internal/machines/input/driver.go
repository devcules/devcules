package input

type Event struct {
	Kind string `json:"kind"`
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
	Key  string `json:"key,omitempty"`
	Text string `json:"text,omitempty"`
}

type Driver interface { Inject(Event) error; Close() error }

type stub struct{}
func (stub) Inject(Event) error { return errUnavailable }
func (stub) Close() error       { return nil }

var errUnavailable = unavailableErr("input driver unavailable on this build")
type unavailableErr string
func (e unavailableErr) Error() string { return string(e) }
