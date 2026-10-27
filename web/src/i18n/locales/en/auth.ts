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
    subtitle: "Use the code the Guildmaster gave you.",
    code: "First-access code",
    login: "Choose a login name",
    password: "Password (at least 12 characters)",
    password2: "Repeat the password",
    pin: "PIN (6 digits)",
    pin2: "Repeat the PIN",
    submit: "Sign in",
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
