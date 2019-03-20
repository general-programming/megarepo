import React from 'react';
import {injectStripe} from 'react-stripe-elements';

class Home extends React.Component {
    constructor(props) {
        super(props);
        this.state = {
            customerID: "",
            customerEmail: ""
        };

        this.handleEmailChange = this.handleEmailChange.bind(this);
        this.handleReferenceChange = this.handleReferenceChange.bind(this);
    }

    onShopClick(productID) {
        this.props.stripe.redirectToCheckout({
            items: [{plan: productID, quantity: 1}],
            clientReferenceId: this.state.customerID,
            customerEmail: this.state.customerEmail,
      
            // Note that it is not guaranteed your customers will be redirected to this
            // URL *100%* of the time, it's possible that they could e.g. close the
            // tab between form submission and the redirect.
            successUrl: 'https://pay.generalprogramming.org/?code=success',
            cancelUrl: 'https://pay.generalprogramming.org/?code=canceled',
          })
          .then(function (result) {
            if (result.error) {
              // If `redirectToCheckout` fails due to a browser or network
              // error, display the localized error message to your customer.
              alert(result.error.message);
            }
          })
    }

    handleEmailChange(e) {
        this.setState({customerEmail: e.target.value});
    }

    handleReferenceChange(e) {
        this.setState({customerID: e.target.value});
    }

    render() {
        let infraItem = "plan_Dshz2OAA2E6ivm";
        let extra = "";

        if (!process.env.NODE_ENV || process.env.NODE_ENV === 'development') {
            infraItem = "pre10";
            extra = " - Development";
        }
        return (
            <div className="home">
                <h1>{"Home" + extra}</h1>
                <p>welcome, if you're here, you know what you are doing</p>
                <p>your card will be billed monthly and account management will be added to here later on,,,</p>
                <p>scream at @everyone for billing modifications</p>
                <p>todo: lol use the stripe api</p>
                <input onChange={this.handleEmailChange} placeholder="email address" />
                <input onChange={this.handleReferenceChange} placeholder="github login" />
                <button id="checkout-button" role="link" onClick={() => this.onShopClick(infraItem)}>Infrastructure Fee $10</button>
            </div>
        );
    }
}

export default injectStripe(Home);
