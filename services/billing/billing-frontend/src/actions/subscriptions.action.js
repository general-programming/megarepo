/* eslint-disable no-shadow */
import { SUBSCRIPTION_TYPES } from './types';

import {
    get as GetSubscriptions,
} from '../services/subscriptions.service';

import { history } from '../helpers/history';

export const getSubscriptions = () => {
    function request() { return { type: SUBSCRIPTION_TYPES.FETCH_SUBSCRIPTION }; }
    function success(subscriptions) { return { type: SUBSCRIPTION_TYPES.SET_SUBSCRIPTIONS, subscriptions }; }
    function failure(error) { return { type: SUBSCRIPTION_TYPES.ERROR, error }; }

    return (dispatch) => {
        dispatch(request());

        GetSubscriptions()
            .then(
                // eslint-disable-next-line no-unused-vars
                (subscriptions) => {
                    console.log(subscriptions);
                    dispatch(success(subscriptions));
                },
                (error) => {
                    dispatch(failure(error.toString()));
                },
            );
    };
};

export const unsubscribe = (user) => {

};
