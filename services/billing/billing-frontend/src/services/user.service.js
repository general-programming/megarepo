/* eslint-disable no-undef */
import { BASE_URL } from '../utils/constants';
import { handleResponse } from '../utils/http';

export const login = (username, password) => {
    const requestOptions = {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
    };

    return fetch(`${BASE_URL}/account/login`, requestOptions)
        .then(handleResponse)
        .then((user) => {
            return user;
        });
};

export const register = (user) => {
    const requestOptions = {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(user),
    };

    return fetch(`${BASE_URL}/account/register`, requestOptions).then(handleResponse);
};

export const logout = () => {
    const requestOptions = {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
    };

    return fetch(`${BASE_URL}/account/logout`, requestOptions)
        .then(handleResponse)
        .then(() => {
            window.location.reload(true);
        });
};

export const getUser = () => {
    const requestOptions = {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' },
    };

    return fetch(`${BASE_URL}/account/`, requestOptions).then(handleResponse);
};
