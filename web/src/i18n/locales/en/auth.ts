import type { Widen } from "../../shape";
import type pt from "../pt-BR/auth";

const auth: Widen<typeof pt> = {
  login: {
    title: "Sign in to the Guild",
    subtitle: "Use the login name and password you created.",
    login: "Login name",
    password: "Password",
    submit: "Sign in",
    firstAccess: "I have a first-access code",
    postChanged: "Your sessions ended because the Guildmaster post changed. Sign in again.",
  },
  setup: {
    title: "Create your Guild",
    subtitle: "You will be the Guildmaster. Use the code shown in the installation terminal.",
    code: "Setup code",
    name: "Your name",
    login: "Login name",
    password: "Password (at least 12 characters)",
    password2: "Repeat the password",
    pin: "PIN (6 digits)",
    pin2: "Repeat the PIN",
    pinWhy: "The PIN confirms sensitive actions, such as touching the vault.",
    submit: "Create the Guild",
  },
  first: {
    title: "First access",
    subtitle:
      "Use the first-access code the Guildmaster gave you, after making your device trust the Mestre.",
    code: "First-access code",
    login: "Choose a login name",
    password: "Password (at least 12 characters)",
    password2: "Repeat the password",
    pin: "PIN (6 to 12 digits)",
    pin2: "Repeat the PIN",
    pinWhy:
      "The PIN confirms sensitive actions. It is not your password: do not use the same value for both.",
    submit: "Sign in",
    errors: {
      invalid_code:
        "That code is no longer valid: it may have expired (it lasts 24 hours) or already been used. Ask the Guildmaster for another.",
      password_too_short:
        "The password needs at least 12 characters. A long, easy-to-remember phrase works.",
      login_taken:
        "Someone already has that login name. Choose another, for example with your surname.",
      invalid_pin: "The PIN needs 6 to 12 digits, no letters.",
      pin_missing:
        "The PIN is missing. On the first access you must choose one (6 to 12 digits); only someone resetting a forgotten password leaves it blank.",
      rate_limited:
        "Too many attempts in a row. Wait {{minutes}} min and try again; if the code no longer works, ask the Guildmaster for another.",
    },
    back: "Back to sign in",
  },
  password: {
    title: "Change the password",
    subtitle: "Your other sessions will be ended.",
    current: "Current password",
    next: "New password (at least 12 characters)",
    next2: "Repeat the new password",
    submit: "Change the password",
    done: "Password changed. The other sessions were ended.",
  },
};
export default auth;
