import React from 'react';
import { Route, Redirect } from 'react-router-dom';
import { connect } from 'react-redux';
import PropTypes from 'prop-types';

import { getUser } from '../actions/user.action';

import LoadingScreen from './LoadingScreen';

// eslint-disable-next-line react/prop-types
const InitialLoader = ({ auth, dispatch, ...rest }) => {
    console.log(auth);

    if (auth.notReady) {
        if (!auth.loading) {
            dispatch(getUser());
        }
        return <LoadingScreen />;
    }

    return rest.children;
};

InitialLoader.propTypes = {
    auth: PropTypes.object.isRequired,
};

const mapStateToProps = state => ({
    auth: state.authentication,
});

export default connect(mapStateToProps)(InitialLoader);
