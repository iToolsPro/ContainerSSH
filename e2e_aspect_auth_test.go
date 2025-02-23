package containerssh_test

import (
	"github.com/containerssh/auth"
	"github.com/containerssh/configuration/v2"
	"github.com/containerssh/http"
	"github.com/containerssh/log"
	"github.com/containerssh/service"
)

func NewAuthTestingAspect() TestingAspect {
	return &authTestingAspect{}
}

type authTestingAspect struct {
}

func (a *authTestingAspect) String() string {
	return "Authentication"
}

func (a *authTestingAspect) Factors() []TestingFactor {
	return []TestingFactor{
		&authTestingFactor{
			aspect:    a,
			passwords: map[string]string{},
		},
	}
}

type authTestingFactor struct {
	aspect    *authTestingAspect
	passwords map[string]string
	lifecycle *SimpleLifecycle
}

func (a *authTestingFactor) Aspect() TestingAspect {
	return a.aspect
}

func (a *authTestingFactor) OnPassword(
	Username string,
	Password []byte,
	_ string,
	_ string,
) (bool, error) {
	if password, ok := a.passwords[Username]; ok &&
		password == string(Password) {
		return true, nil
	}
	return false, nil
}

func (a *authTestingFactor) OnPubKey(
	_ string,
	_ string,
	_ string,
	_ string,
) (bool, error) {
	return false, nil
}

func (a *authTestingFactor) String() string {
	return "In-Memory"
}

func (a *authTestingFactor) ModifyConfiguration(config *configuration.AppConfig) error {
	config.Auth.URL = "http://127.0.0.1:8080"
	return nil
}

func (a *authTestingFactor) StartBackingServices(
	_ configuration.AppConfig, logger log.Logger,
) error {
	srv, err := auth.NewServer(
		http.ServerConfiguration{
			Listen: "127.0.0.1:8080",
		},
		a,
		logger,
	)
	if err != nil {
		return err
	}
	a.lifecycle = NewSimpleLifecycle(service.NewLifecycle(srv))
	return a.lifecycle.Start()
}

func (a *authTestingFactor) GetSteps(_ configuration.AppConfig, _ log.Logger) []Step {
	return []Step{
		{
			Match: `^I create(?:|d) the user "([^"]*)" with the password "([^"]*)"$`,
			Method: func(user string, password string) error {
				a.passwords[user] = password
				return nil
			},
		},
		{
			Match: `^I delete(?:|d) user "([^"]*)"$`,
			Method: func(user string) error {
				delete(a.passwords, user)
				return nil
			},
		},
	}
}

func (a *authTestingFactor) StopBackingServices(_ configuration.AppConfig, _ log.Logger) error {
	return a.lifecycle.Stop()
}
