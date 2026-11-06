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

func (j *jobObject) alive() uint32 {
	return 0
}

func (j *jobObject) close() {
}
