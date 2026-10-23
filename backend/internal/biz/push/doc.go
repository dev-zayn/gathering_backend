// Package push carries the inbox to the players' phones: every notification
// the inbox gains, once its write has committed, is sent through Firebase
// to each phone of its recipient whose session is live. A raise never waits
// for it and a failure never undoes a notification: the inbox stays the
// record. Without a Firebase key a push is a "would push" line in the log.
// docs/PUSH.md is the contract.
package push
