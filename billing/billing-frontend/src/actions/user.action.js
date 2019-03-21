import { USER_TYPES } from './types';

import { history } from "../helpers/history"

import { 
    login as LoginUser, 
    logout as LogoutUser, 
    register as RegisterUser, 
} from '../services/user.service'

export const login = (username, password) => {
    return dispatch => {
        dispatch(request({ username }));

        LoginUser(username, password)
            .then(
                user => { 
                    dispatch(success(user));
                    history.push('/');
                },
                error => {
                    dispatch(failure(error.toString()));
                }
            );
    };

    function request(user) { return { type: USER_TYPES.LOGIN_REQUEST, user } }
    function success(user) { return { type: USER_TYPES.LOGIN_SUCCESS, user } }
    function failure(error) { return { type: USER_TYPES.LOGIN_FAILURE, error } }
}

export const logout = () => {
    LogoutUser();
    return { type: USER_TYPES.LOGOUT };
}

export const register = (user) => {
    return dispatch => {
        dispatch(request(user));

        RegisterUser(user)
            .then(
                user => { 
                    dispatch(success());
                    history.push('/login');
                },
                error => {
                    dispatch(failure(error.toString()));
                }
            );
    };

    function request(user) { return { type: USER_TYPES.REGISTER_REQUEST, user } }
    function success(user) { return { type: USER_TYPES.REGISTER_SUCCESS, user } }
    function failure(error) { return { type: USER_TYPES.REGISTER_FAILURE, error } }
}

