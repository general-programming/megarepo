import React, { Component } from 'react';

import { BrowserRouter as Router, Route, Link } from 'react-router-dom';
import {StripeProvider, Elements} from 'react-stripe-elements';

import logo from './logo.svg';
import './App.css';

import Header from './components/Header';
import Payment from './components/Payment';

class App extends Component {
  render() {
    return (
      <div className="App">
        <StripeProvider apiKey="pk_test_s8Uv3fvUXxJfyMVSdVYq4KZK">
          <Elements>
            <Router>
              <Header />
              <Payment/>
            </Router>
          </Elements>
        </StripeProvider>
      </div>
    );
  }
}

export default App;
