module Credential exposing (Credential)

{-| A username and the password that goes with it, in the shape the browser's credential
store takes them: `id` is the name `PasswordCredential` gives the username.

Magnes offers one through `Main.storeCredential` after a registration, a sign-in, or a
password change bitmagnet accepted. After a password change whose answer was lost, the login
form is opened on one, so that the new password is tried first; see `PasswordChange`.

-}


type alias Credential =
    { id : String
    , password : String
    }
