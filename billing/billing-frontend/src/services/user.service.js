/* eslint-disable no-undef */
import { BASE_URL } from '../utils/constants';

function handleResponse(response) {
    return response.text().then((text) => {
        const data = text && JSON.parse(text);
        if (!response.ok) {
            if (response.status === 401) {
                // Reload if we are logged out from the server.
                window.location.reload(true);
            }

            const error = ((data && data.message) || data.error) || response.statusText;
            return Promise.reject(error);
        }

        return data;
    });
}

export const login = (username, password) => {
    const requestOptions = {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
    };

    return fetch(`${BASE_URL}/account/login/`, requestOptions)
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

    return fetch(`${BASE_URL}/account/register/`, requestOptions).then(handleResponse);
};

export const logout = () => {
    const requestOptions = {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
    };

    return fetch(`${BASE_URL}/account/logout/`, requestOptions)
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
