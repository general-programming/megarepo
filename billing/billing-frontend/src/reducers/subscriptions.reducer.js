import { SUBSCRIPTION_TYPES } from '../actions/types';

const initialState = { subscriptions: {}, loaded: false };

export function subscriptions(state = initialState, action) {
    switch (action.type) {
        case SUBSCRIPTION_TYPES.FETCH_SUBSCRIPTIONS:
            return {
                ...state,
                fetching: true,
                subscriptions: { ...state.subscriptions },
                loaded: state.loaded,
            };
        case SUBSCRIPTION_TYPES.SET_SUBSCRIPTIONS:
            return {
                ...state,
                subscriptions: { ...action.subscriptions },
                loaded: true,
            };
        case SUBSCRIPTION_TYPES.ERROR:
            return {
                ...state,
                error: action.error,
                loaded: true,
            };
        default:
            return state;
    }
}
