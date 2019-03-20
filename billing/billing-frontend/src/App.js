import React, { Component } from 'react';

import { BrowserRouter as Router, Route, Link } from 'react-router-dom';
import {StripeProvider, Elements} from 'react-stripe-elements';

import logo from './logo.svg';
import './App.css';

import Header from './components/Header';
import Home from './components/Home';
import Payment from './components/Payment';

const stripeBetas = ['checkout_beta_4'];
let stripeKey = "";

if (!process.env.NODE_ENV || process.env.NODE_ENV === 'development') {
  stripeKey = "pk_test_s8Uv3fvUXxJfyMVSdVYq4KZK";
} else {
  stripeKey = "pk_live_jMaLiRMATTwNk89wb7p4OgUT";
}

class App extends Component {
  render() {
    return (
      <div className="App">
        <StripeProvider apiKey={stripeKey} betas={stripeBetas}>
          <Elements>
            <Router>
              <Header />
              { /* "XXX: Handle stripe/success, stripe/canceled" */ }
              <Route path="/" exact component={Home} />
              <Route path="/pay" exact component={Payment} />
            </Router>
          </Elements>
        </StripeProvider>
      </div>
    );
  }
}

export default App;
