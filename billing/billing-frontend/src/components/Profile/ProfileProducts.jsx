import React from 'react';
import withStyles from '@material-ui/core/styles/withStyles';
import { connect } from 'react-redux';
import Typography from '@material-ui/core/Typography';

import ProductItem from './ProductItem';

const styles = theme => ({
    container: {
        paddingTop: theme.spacing.unit * 2,
    },
});

function ProfileProducts(props) {
    const { classes } = props;

    return (
        <div className={classes.container}>
            <Typography variant="h4" gutterBottom component="h2">
                Your products
            </Typography>
            <ProductItem />
            <ProductItem />
            <ProductItem />
            <ProductItem />
            <ProductItem />
        </div>
    );
}

function mapStateToProps(state) {
    /*const { products } = state.products;
    return {
        products,
    };*/
}


export default connect(mapStateToProps)(withStyles(styles)(ProfileProducts));
