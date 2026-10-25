//go:build !windows

package player

import "errors"

// Non-Windows builds are used for automated tests; never store tokens unencrypted.
func protectSecret(b []byte) ([]byte, error) {
	return nil, errors.New("Захищене сховище сесії доступне у Windows")
}
func unprotectSecret(b []byte) ([]byte, error) {
	return nil, errors.New("Захищене сховище сесії доступне у Windows")
}
func registerBridge(string) error {
	return errors.New("Налаштування розширення доступне у Windows")
}
func unregisterBridge(string) error {
	return errors.New("Налаштування розширення доступне у Windows")
}
func bridgeRegistered(string, string) bool { return false }
func revealFolder(string) error {
	return errors.New("Відкриття папки доступне у Windows")
}
