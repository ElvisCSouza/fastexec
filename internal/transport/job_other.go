//go:build !windows

package transport

type jobObject struct{}

func newJobObject() (*jobObject, error) {
	return &jobObject{}, nil
}

func (j *jobObject) assign(pid int) error {
	return nil
}

func (j *jobObject) kill() {
}

func (j *jobObject) close() {
}
