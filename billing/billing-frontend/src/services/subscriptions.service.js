/* eslint-disable no-undef */
import { BASE_URL } from '../utils/constants';
import { handleResponse } from '../utils/http';

export const get = () => {
    const requestOptions = {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' },
    };

    return fetch(`${BASE_URL}/subscriptions`, requestOptions)
        .then(handleResponse)
        .then((response) => {
            return response;
        });
};
