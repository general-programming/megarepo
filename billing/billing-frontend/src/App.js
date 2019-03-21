import React, { Component } from 'react';

import { BrowserRouter as Router, Route, Link } from 'react-router-dom';
import { StripeProvider, Elements } from 'react-stripe-elements';

import { MuiThemeProvider, createMuiTheme } from '@material-ui/core/styles';

import './App.css';

import AppHeader from './components/AppHeader';
import HomeView from './views/HomeView';
import LoginView from './views/LoginView';

import {
  COLOR_LIGHT, 
  COLOR_DARK, 
  COLOR_PRIMARY, 
  COLOR_TEXT
} from './utils/constants';

const stripeBetas = ['checkout_beta_4'];
let stripeKey = "";

if (!process.env.NODE_ENV || process.env.NODE_ENV === 'development') {
  stripeKey = "pk_test_s8Uv3fvUXxJfyMVSdVYq4KZK";
} else {
  stripeKey = "pk_live_jMaLiRMATTwNk89wb7p4OgUT";
}

const theme = createMuiTheme({
  palette: {
    primary: {
      main: '#FFCC4D',
    },
    secondary: {
      main: '#F4900C',
    }
  },
});

class App extends Component {
  render() {
    return (
      <MuiThemeProvider theme={theme}>
        <StripeProvider apiKey={stripeKey} betas={stripeBetas}>
          <Elements>
            <Router>
              <AppHeader />
              <Route path="/" exact component={ HomeView } />
              <Route path="/login" exact component={ LoginView } />
            </Router>
          </Elements>
        </StripeProvider>
      </MuiThemeProvider>
    );
  }
}

export default App;
