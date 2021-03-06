/* eslint-disable react/jsx-filename-extension */
/* eslint-disable react/prefer-stateless-function */
import React, { Component } from 'react';

import { Router, Route } from 'react-router-dom';
import { StripeProvider, Elements } from 'react-stripe-elements';

import CssBaseline from '@material-ui/core/CssBaseline';
import { MuiThemeProvider, createMuiTheme } from '@material-ui/core/styles';

import './App.css';

import { history } from './helpers/history';

import InitialLoader from './components/InitialLoader';
import PrivateRoute from './components/PrivateRoute';

import HomePage from './components/Home/HomePage';
import LoginPage from './components/Login/LoginPage';
import RegisterPage from './components/Register/RegisterPage';
import ProfilePage from './components/Profile/ProfilePage';

const stripeBetas = ['checkout_beta_4'];
let stripeKey = '';

if (!process.env.NODE_ENV || process.env.NODE_ENV === 'development') {
    stripeKey = 'pk_test_s8Uv3fvUXxJfyMVSdVYq4KZK';
} else {
    stripeKey = 'pk_live_jMaLiRMATTwNk89wb7p4OgUT';
}

const theme = createMuiTheme({
    palette: {
        primary: {
            main: '#FFCC4D',
        },
        secondary: {
            main: '#F4900C',
        },
    },
});

class App extends Component {
    render() {
        return (
            <MuiThemeProvider theme={theme}>
                <StripeProvider apiKey={stripeKey} betas={stripeBetas}>
                    <Elements>
                        <>
                            <CssBaseline />
                            <InitialLoader>
                                <Router history={ history }>
                                    <PrivateRoute path="/" exact component={HomePage} />
                                    <PrivateRoute path="/profile" exact component={ProfilePage} />

                                    <Route path="/login" exact component={LoginPage} />
                                    <Route path="/register" exact component={RegisterPage} />
                                </Router>
                            </InitialLoader>
                        </>
                    </Elements>
                </StripeProvider>
            </MuiThemeProvider>
        );
    }
}

export default App;
