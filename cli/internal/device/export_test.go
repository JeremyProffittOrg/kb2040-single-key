package device

// AutodetectFromForTest exposes the probe loop so the two-pass behaviour can be tested with
// a fake opener, without needing real serial ports.
var AutodetectFromForTest = autodetectFrom
