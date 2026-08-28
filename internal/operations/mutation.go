package operations

import (
	"context"
	"fmt"

	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/config"
)

// ServiceAction is one authorized Docker Compose lifecycle operation.
type ServiceAction = compose.ServiceAction

const (
	ActionRestart = compose.ActionRestart
	ActionStop    = compose.ActionStop
	ActionStart   = compose.ActionStart
)

// Script is one exact configured remote command. It has no runtime arguments;
// variants with different flags are separate configured names.
type Script = config.Script

// ActionPreview returns the exact command a typed action will run. It exists
// for the human confirmation menu; execution still goes through Action.
func (o *HostOperator) ActionPreview(action ServiceAction, service string) string {
	return compose.ActionCommand(o.composeDir, action, service)
}

// Action validates a current project service and starts one lifecycle command.
func (o *HostOperator) Action(ctx context.Context, action ServiceAction, service string) (Feed, error) {
	if !validAction(action) {
		return Feed{}, InvalidActionError{Action: int(action)}
	}
	if err := o.requireService(ctx, service); err != nil {
		return Feed{}, err
	}
	return o.startFeed(ctx, o.ActionPreview(action, service), stdoutLine)
}

// Script starts one exact configured command by name.
func (o *HostOperator) Script(ctx context.Context, name string) (Feed, error) {
	command, err := configuredScript(o.scripts, name)
	if err != nil {
		return Feed{}, err
	}
	return o.startFeed(ctx, command, stdoutLine)
}

func configuredScript(scripts map[string]string, name string) (string, error) {
	command, ok := scripts[name]
	if !ok {
		return "", UnknownScriptError{Name: name}
	}
	return command, nil
}

func validAction(action ServiceAction) bool {
	switch action {
	case ActionRestart, ActionStop, ActionStart:
		return true
	default:
		return false
	}
}

type InvalidActionError struct {
	Action int
}

func (e InvalidActionError) Error() string {
	return fmt.Sprintf("unknown service action %d", e.Action)
}

type UnknownScriptError struct {
	Name string
}

func (e UnknownScriptError) Error() string {
	return fmt.Sprintf("script %q is not configured", e.Name)
}
