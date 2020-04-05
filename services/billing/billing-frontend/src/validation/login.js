const Validator = require('validator');
const isEmpty = require('./is-empty');

module.exports = function validateLoginInput(data) {
    const errors = {};
    const d = {};

    d.username = !isEmpty(data.username) ? data.username : '';
    d.password = !isEmpty(data.password) ? data.password : '';

    if (Validator.isEmpty(data.username)) {
        errors.email = 'Username field is required';
    }

    if (Validator.isEmpty(data.password)) {
        errors.password = 'Password field is required';
    }

    return {
        errors,
        isValid: isEmpty(errors),
    };
};
