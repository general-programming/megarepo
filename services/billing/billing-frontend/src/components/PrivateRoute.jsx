import React from 'react';
import { Route, Redirect } from 'react-router-dom';
import { connect } from 'react-redux';
import PropTypes from 'prop-types';

// eslint-disable-next-line react/prop-types
const PrivateRoute = ({ component: Component, auth, ...rest }) => (
    <Route
        {...rest}
        render={props => (auth.loggedIn === true ? (
            <Component {...props} />
        ) : (
            <Redirect to="/login" />
        ))
        }
    />
);

PrivateRoute.propTypes = {
    auth: PropTypes.object.isRequired,
};

const mapStateToProps = state => ({
    auth: state.authentication,
});

export default connect(mapStateToProps)(PrivateRoute);
