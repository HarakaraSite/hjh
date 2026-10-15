package contracts

import "strconv"

// AssistantKey keeps retries separate without changing the logical model step.
func (request RequestIdentity) AssistantKey(suffix string) string {
	prefix := request.Lane + "/" + strconv.Itoa(request.RequestOrdinal)
	if request.RetryAttempt != 0 {
		prefix += "/retry/" + strconv.Itoa(request.RetryAttempt)
	}
	return prefix + "/" + suffix
}
