package app

// testMachineID is a fixed fixture machine identity passed through
// engine.WithFixtureMachineIdentity, so local-route tests never depend on the
// CI host's real machine identity (unavailable off darwin by design).
const testMachineID = "00112233445566778899aabbccddeeff"
