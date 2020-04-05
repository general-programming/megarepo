import { USER_TYPES } from '../actions/types';

export function registration(state = {}, action) {
    switch (action.type) {
        case USER_TYPES.REGISTER_REQUEST:
            return { registering: true };
        case USER_TYPES.REGISTER_SUCCESS:
            return {};
        case USER_TYPES.REGISTER_FAILURE:
            return {};
        default:
            return state;
    }
}