/* eslint-disable no-shadow */
import { USER_TYPES } from './types';

import {
    login as LoginUser,
    logout as LogoutUser,
    register as RegisterUser,
    getUser as GetUser,
} from '../services/user.service';

import { getSubscriptions } from './subscriptions.action';

import { history } from '../helpers/history';

export const register = (user) => {
    function request(user) { return { type: USER_TYPES.REGISTER_REQUEST, user }; }
    function success(user) { return { type: USER_TYPES.REGISTER_SUCCESS, user }; }
    function failure(error) { return { type: USER_TYPES.REGISTER_FAILURE, error }; }

    return (dispatch) => {
        dispatch(request(user));

        RegisterUser(user)
            .then(
                // eslint-disable-next-line no-unused-vars
                (user) => {
                    dispatch(success());
                    history.push('/login');
                },
                (error) => {
                    dispatch(failure(error.toString()));
                },
            );
    };
};

export const login = (username, password) => {
    function request(user) { return { type: USER_TYPES.LOGIN_REQUEST, user }; }
    function success(user) { return { type: USER_TYPES.LOGIN_SUCCESS, user }; }
    function failure(error) { return { type: USER_TYPES.LOGIN_FAILURE, error }; }

    return (dispatch) => {
        dispatch(request({ username }));

        LoginUser(username, password)
            .then(
                (user) => {
                    dispatch(success(user));
                    dispatch(getSubscriptions());
                    history.push('/');
                },
                (error) => {
                    dispatch(failure(error.toString()));
                },
            );
    };
};

export const logout = () => {
    LogoutUser();
    return { type: USER_TYPES.LOGOUT };
};

export const getUser = () => {
    function request() { return { type: USER_TYPES.INIT }; }
    function success(user) { return { type: USER_TYPES.LOGIN_SUCCESS, user }; }
    function failure(error) { return { type: USER_TYPES.LOGIN_FAILURE, error }; }

    return (dispatch) => {
        dispatch(request());

        GetUser()
            .then(
                (user) => {
                    dispatch(getSubscriptions());
                    dispatch(success(user));
                },
                (error) => {
                    if (error.toString() === "You are not logged in.") {
                        dispatch(failure());
                        return;
                    }

                    dispatch(failure(error.toString()));
                },
            );
    };
};
