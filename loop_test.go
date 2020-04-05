package geloop_test

import (
	"testing"

	"github.com/roy2220/geloop"
	"github.com/stretchr/testify/assert"
)

func TestLoopStop(t *testing.T) {
	l := new(geloop.Loop).Init()
	err := l.Open()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	l.Stop()
	l.Run()
	err = l.Close()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	l.Stop()
}
