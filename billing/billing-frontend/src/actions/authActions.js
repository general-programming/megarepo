import { URL_SERVER } from "../utils/constants";

import { GET_ERRORS, SET_CURRENT_USER, CLEAR_CURRENT_USER } from "./types";

// Login - Get User Token
export const loginUser = userData => dispatch => {
    var url = URL_SERVER + "/users/login"; // URL
    fetch(url, {
        method: "POST",
        body: JSON.stringify(userData),
        headers: {
            "Content-Type": "application/json; charset=utf-8"
        }
    })
    .then(res => res.json())
    .then(response => {
        const { password, name } = response;
        // Set user in localstorage
        localStorage.setItem("user", JSON.stringify(response));
        // Dispatch that the user was changed
        dispatch(setCurrentUser(name))
    })
    .catch(err => {
        dispatch({
            type: GET_ERRORS,
            payload: err
        });
    });
};
  
// Set logged in user
export const setCurrentUser = decoded => {
    return {
        type: SET_CURRENT_USER,
        payload: decoded
    };
};
  
// Log user out
export const logoutUser = () => dispatch => {
    // Remove token from localStorage
    localStorage.removeItem("user");
    // Remove auth header for future requests
    // setAuthToken(false);
    // Set current user to {} which will set isAuthenticated to false
    dispatch(setCurrentUser({}));
};

export const clearCurrentProfile = () => {
    return {
        type: CLEAR_CURRENT_USER
    };
};