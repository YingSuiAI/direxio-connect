//go:build !linux

package main

import "errors"

func executeSupervisorInvocation(supervisorInvocation) error {
	return errors.New("host-supervised Connector processes require Linux")
}
