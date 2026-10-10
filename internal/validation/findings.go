// Package validation collects bounded independent diagnostics without changing
// the acceptance predicates of the validators that produce them.
package validation

import (
	"errors"
	"fmt"
)

const MaximumFindings = 32

var errLimited = errors.New("validation findings limited to 32; additional checks may be omitted")

type Findings struct {
	items [MaximumFindings]error
	n     int
}

func (f *Findings) Add(err error) {
	f.AddContext("", err)
}

// AddContext labels each independent finding before joining, so a nested group
// cannot hide many findings behind one slot in the diagnostic budget.
func (f *Findings) AddContext(label string, err error) {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, item := range joined.Unwrap() {
			f.AddContext(label, item)
			if f.Full() {
				break
			}
		}
		return
	}
	if err != nil && !f.Full() {
		if label != "" {
			err = fmt.Errorf("%s: %w", label, err)
		}
		f.items[f.n] = err
		f.n++
	}
}

func (f *Findings) Full() bool { return f.n == MaximumFindings }

func (f *Findings) Err() error {
	if f.Full() {
		return errors.Join(errors.Join(f.items[:f.n]...), errLimited)
	}
	return errors.Join(f.items[:f.n]...)
}
