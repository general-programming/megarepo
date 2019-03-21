import { USER_TYPES } from "../actions/types";

let user = JSON.parse(localStorage.getItem('user'));
const initialState = user ? { loggedIn: true, user } : {};

export function authentication(state = initialState, action) {
    switch (action.type) {
        case USER_TYPES.LOGIN_REQUEST:
            return {
                loggingIn: true,
                user: action.user
            };
        case USER_TYPES.LOGIN_SUCCESS:
            return {
                loggedIn: true,
                user: action.user
            };
        case USER_TYPES.LOGIN_FAILURE:
            return {
                error: action.error
            };
        case USER_TYPES.LOGOUT:
            return {};
        default:
            return state
    }
}