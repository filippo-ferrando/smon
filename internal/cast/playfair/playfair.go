// Package playfair wraps the FairPlay key decryption used by open AirPlay
// receivers (UxPlay/RPiPlay). Vendored C from UxPlay lib/playfair, GPL-3.
package playfair

// #cgo LDFLAGS: -lm
// #include "playfair.h"
import "C"
import "unsafe"

// Decrypt returns the 16-byte AES key a receiver derives from the 164-byte
// fp-setup message and the 72-byte ekey.
func Decrypt(message3 [164]byte, ekey [72]byte) (key [16]byte) {
	C.playfair_decrypt((*C.uchar)(unsafe.Pointer(&message3[0])), (*C.uchar)(unsafe.Pointer(&ekey[0])), (*C.uchar)(unsafe.Pointer(&key[0])))
	return key
}
