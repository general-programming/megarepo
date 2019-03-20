import React from 'react';
import ReactDOM from 'react-dom';

import { Provider } from "react-redux";

import './index.css';
import App from './App';
import * as serviceWorker from './serviceWorker';

import { setCurrentUser } from './actions/authActions';

import store from "./store/store";

import 'typeface-roboto';

ReactDOM.render(
    <Provider store={store}>
        <App />
    </Provider>,
    document.getElementById('root')
);


if (localStorage.user) {
    try{
        const user = JSON.parse(localStorage.user);
        store.dispatch(setCurrentUser(user.name));
    } catch(error) {
        console.error(error);
        // expected output: ReferenceError: nonExistentFunction is not defined
        // Note - error messages will vary depending on browser
    }
}

// If you want your app to work offline and load faster, you can change
// unregister() to register() below. Note this comes with some pitfalls.
// Learn more about service workers: https://bit.ly/CRA-PWA
serviceWorker.unregister();
