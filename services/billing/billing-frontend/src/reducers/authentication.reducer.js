import { USER_TYPES } from '../actions/types';

const initialState = { notReady: true, loading: false };

export function authentication(state = initialState, action) {
    switch (action.type) {
        case USER_TYPES.LOGIN_REQUEST:
            return {
                loggingIn: true,
                user: action.user,
            };
        case USER_TYPES.LOGIN_SUCCESS:
            return {
                loggedIn: true,
                user: action.user,
            };
        case USER_TYPES.LOGIN_FAILURE:
            return {
                error: action.error,
            };
        case USER_TYPES.LOGOUT:
            return {};
        case USER_TYPES.INIT:
            return { notReady: true, loading: true };
        default:
            return state;
    }
}
