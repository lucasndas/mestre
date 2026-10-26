import type { Widen } from "../../shape";
import type pt from "../pt-BR/errors";

const errors: Widen<typeof pt> = {
  network: "I could not reach the Citadel. Check the connection and try again.",
  invalid_credentials: "Invalid user or password.",
  login_blocked: "Too many wrong passwords. Try again in {{minutes}} min.",
  rate_limited: "Too many attempts. Try again in {{minutes}} min.",
  unauthenticated: "Your session is over. Sign in again.",
  forbidden_level: "Your level does not allow this action.",
  bad_origin: "This is not the address of the panel. Open the panel from the Guild address.",
  pin_required: "This action asks for your PIN.",
  pin_invalid: "Wrong PIN.",
  pin_locked: "PIN locked after repeated errors. Your session was ended.",
  no_pin: "You do not have a PIN yet.",
  invalid_code: "Invalid or expired code.",
  password_too_short: "The password needs at least {{min}} characters.",
  password_wrong: "The current password is wrong.",
  invalid_login: "The login name must have letters and numbers, with no spaces.",
  invalid_pin: "The PIN must be 6 digits.",
  login_taken: "That login name already exists.",
  bad_request: "I did not understand the request. Check the fields.",
  not_found: "Not found.",
  unknown_topic: "Unknown topic.",
  unavailable: "The Citadel is not available right now. Try again shortly.",
  internal: "Something went wrong in the Citadel. Try again.",
  mismatch_password: "The two passwords are not the same.",
  mismatch_pin: "The two PINs are not the same.",
  required: "Fill in this field.",
};
export default errors;
