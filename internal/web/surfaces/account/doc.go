// Package account is the surface that creates the first account, signs a reader in and out,
// and resets a forgotten password with the key file. The core's middleware depends on its
// pages by name (web.PageSignIn): a refusal that leaves a reader with no session is drawn
// on the sign-in page.
package account
