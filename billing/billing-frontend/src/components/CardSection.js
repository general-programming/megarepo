// CardSection.js
import React from 'react';
import {CardElement} from 'react-stripe-elements';

const elementStyles = {
    base: {
      color: '#32325D',
      fontWeight: 500,
      fontFamily: 'Source Code Pro, Consolas, Menlo, monospace',
      fontSize: '16px',
      fontSmoothing: 'antialiased',

      '::placeholder': {
        color: '#CFD7DF',
      },
      ':-webkit-autofill': {
        color: '#e39f48',
      },
    },
    invalid: {
      color: '#E25950',

      '::placeholder': {
        color: '#FFCCA5',
      },
    },
};

var elementClasses = {
    focus: 'focused',
    empty: 'empty',
    invalid: 'invalid',
  };


class CardSection extends React.Component {
  render() {
    return (
      <label>
        Card details
        <CardElement style={elementStyles} classes={elementClasses} />
      </label>
    );
  }
}

export default CardSection;
