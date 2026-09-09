package operationalruntime

// newInvocationAdmissionBlocked reports whether this process may create a new
// model invocation. The optional limit is process-local by design: restarting
// the daemon creates a fresh, explicitly configured allowance while durable
// invocation state remains authoritative in MongoDB.
func (service *ProductionService) newInvocationAdmissionBlocked() bool {
	service.admissionMu.Lock()
	defer service.admissionMu.Unlock()
	return service.newInvocationAdmissionBlockedLocked()
}

func (service *ProductionService) newInvocationAdmissionBlockedLocked() bool {
	return service.suspendNewInvocations || service.admissionLimitEnabled && service.admissionRemaining == 0
}

// beginNewInvocationAdmission serializes the check and authorization of a new
// invocation. The returned release function consumes one allowance only when
// the authorization command actually creates the durable invocation.
func (service *ProductionService) beginNewInvocationAdmission() (func(bool), error) {
	service.admissionMu.Lock()
	if service.newInvocationAdmissionBlockedLocked() {
		service.admissionMu.Unlock()
		return nil, errNewInvocationAdmissionSuspended
	}
	return func(created bool) {
		if created && service.admissionLimitEnabled {
			service.admissionRemaining--
		}
		service.admissionMu.Unlock()
	}, nil
}
