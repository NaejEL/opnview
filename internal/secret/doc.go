// Package secret holds the key file beside the database and the authenticated
// encryption of the credentials opnview has to be able to send again.
//
// WHY ENCRYPTION AND NOT HASHING, stated once here because it is the thing most
// likely to be "corrected" by somebody who knows that passwords are hashed.
// ROADMAP.md, "Rules that apply to every step": the OPNsense secret and the
// MaxMind key are NOT passwords. They are sent to those services on every call,
// so hashing them would make the product unable to work. They are encrypted at
// rest and decrypted to be used. The account password is the other thing, it is
// hashed, and it lives in internal/auth.
//
// WHY THIS IS NOT A SEAM. One AEAD is enough and a registry for it would be
// machinery for a problem nobody has. The mitigation is the same one the password
// record uses: an algorithm LABEL and a KEY IDENTIFIER are stored beside every
// ciphertext, so a future scheme is a second label rather than a second copy of
// the product, and a key that has been replaced is distinguishable from a
// ciphertext that has been tampered with.
//
// THE LIMIT OF WHAT THE KEY FILE PROTECTS is stated in README.md's limitations
// section, in ROADMAP.md's own words. It is not restated on any screen, and no
// string in this package is user-visible.
package secret
