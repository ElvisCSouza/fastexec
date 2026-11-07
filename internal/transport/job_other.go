//go:build !windows

package transport

type jobObject struct{}

func newJobObject() (*jobObject, error) {
	return &jobObject{}, nil
}

// newExecJob: fora do Windows não há job nomeado nem handoff.
func newExecJob(bool) (*jobObject, string, error) {
	j, err := newJobObject()
	return j, "", err
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
