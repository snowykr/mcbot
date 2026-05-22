//go:build !unix

package cli

func defaultDetectCurrentOwnership() (ownershipPair, bool) {
	return ownershipPair{}, false
}

func defaultDetectPathOwnership(path string) (ownershipPair, bool, error) {
	return ownershipPair{}, false, nil
}
