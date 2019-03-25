const Validator = require('validator');
const isEmpty = require('./is-empty');

module.exports = function validateRegisterInput(data) {
    const errors = {};
    const d = {};

    d.username = !isEmpty(data.username) ? data.username : '';
    d.email = !isEmpty(data.email) ? data.email : '';
    d.password = !isEmpty(data.password) ? data.password : '';
    d.password_verify = !isEmpty(data.password_verify) ? data.password_verify : '';

    if (Validator.isEmpty(data.username)) {
        errors.name = 'Username field is required';
    }

    if (!Validator.isLength(data.username, { min: 2, max: 64 })) {
        errors.name = 'Username must be between 2 and 64 characters';
    }

    if (Validator.isEmpty(data.email)) {
        errors.email = 'Email field is required';
    }

    if (!Validator.isEmail(data.email)) {
        errors.email = 'Email is invalid';
    }

    if (Validator.isEmpty(data.password)) {
        errors.password = 'Password field is required';
    }

    if (!Validator.isLength(data.password, { min: 6, max: 512 })) {
        errors.password = 'Password must be at least 6 characters and less than 512';
    }

    if (Validator.isEmpty(data.password_verify)) {
        errors.password_verify = 'Confirm Password field is required';
    }

    if (!Validator.equals(data.password, data.password_verify)) {
        errors.password_verify = 'Passwords must match';
    }

    return {
        errors,
        isValid: isEmpty(errors),
    };
};
